package proxy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"zongheng-vpn/shared/paths"
)

// EngineLogHealth reports observability of the authenticated current child,
// independently of engine readiness and proxy restoration. Old children and
// ordinary library RunEngine callers return unknown; missing data is not
// evidence that logging works.
type EngineLogHealth struct {
	State string        `json:"state"`
	Code  EngineLogCode `json:"code"`
}

const engineLogStatusVersion = 1

type engineLogStatusRequest struct {
	Version  int            `json:"version"`
	Command  string         `json:"command"`
	Nonce    string         `json:"nonce"`
	Identity EngineIdentity `json:"identity"`
	MAC      string         `json:"mac"`
}

type engineLogStatusResponse struct {
	Version  int             `json:"version"`
	Command  string          `json:"command"`
	Nonce    string          `json:"nonce"`
	Identity EngineIdentity  `json:"identity"`
	Health   EngineLogHealth `json:"health"`
	MAC      string          `json:"mac"`
}

func signEngineLogStatus(secret, purpose string, value any) string {
	key, _ := hex.DecodeString(secret)
	encoded, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("zhvpn-engine-log-status-v1\x00" + purpose + "\x00"))
	_, _ = mac.Write(encoded)
	return hex.EncodeToString(mac.Sum(nil))
}

func validEngineLogHealth(health EngineLogHealth) bool {
	switch health.State {
	case "healthy", "unknown":
		return health.Code == ""
	case "degraded":
		return IsEngineLogHealthCode(health.Code)
	}
	return false
}

func decodeEngineLogStatus(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrControlIdentity
	}
	return nil
}

// InspectEngineLog never merges the health of a replaced child with an earlier
// Inspect result. The caller must supply that exact authenticated identity.
func InspectEngineLog(ctx paths.Context, expected EngineIdentity) (EngineLogHealth, error) {
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	record, err := loadRecord(ctx)
	if err != nil || expected != record.Identity {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	nonce, err := randomHex(32)
	if err != nil {
		return EngineLogHealth{State: "unknown"}, ErrControlUnavailable
	}
	request := engineLogStatusRequest{Version: engineLogStatusVersion, Command: "log-status", Nonce: nonce, Identity: expected}
	request.MAC = signEngineLogStatus(record.Secret, "request", request)
	encoded, _ := json.Marshal(request)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodPost, "http://"+record.Address+"/v1/log-status", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return EngineLogHealth{State: "unknown"}, ErrControlUnavailable
	}
	defer response.Body.Close()
	current, readErr := loadRecord(ctx)
	if readErr != nil || current.Identity != expected || current.Address != record.Address || current.Secret != record.Secret {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	if response.StatusCode == http.StatusNotFound {
		return EngineLogHealth{State: "unknown"}, nil
	}
	if response.StatusCode != http.StatusOK {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	var result engineLogStatusResponse
	content, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	if err != nil || len(content) > 2048 || decodeEngineLogStatus(bytes.NewReader(content), &result) != nil {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	mac := result.MAC
	result.MAC = ""
	if result.Version != engineLogStatusVersion || result.Command != request.Command || result.Nonce != nonce || result.Identity != expected || !validEngineLogHealth(result.Health) || !validMAC(signEngineLogStatus(record.Secret, "response", result), mac) {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	// A matching response may race an instance replacement in the state file.
	current, err = loadRecord(ctx)
	if err != nil || current.Identity != expected || current.Address != record.Address || current.Secret != record.Secret {
		return EngineLogHealth{State: "unknown"}, ErrControlIdentity
	}
	return result.Health, nil
}

func (c *engineControl) setEngineLog(log *EngineLog) {
	if log == nil {
		c.setEngineRecorder(nil)
		return
	}
	c.setEngineRecorder(newEngineLogRecorder(log))
}

func (c *engineControl) setEngineRecorder(recorder *engineLogRecorder) {
	c.logRecorder.Store(recorder)
	if recorder != nil {
		recorder.bindIdentity(c.record.Identity)
		c.engineLog.Store(recorder.log)
	} else {
		c.engineLog.Store(nil)
	}
}

func (c *engineControl) logHealth() EngineLogHealth {
	log := c.engineLog.Load()
	state := log.State()
	return EngineLogHealth{State: state.State, Code: state.Code}
}

func (c *engineControl) appendLog(event EngineLogEventKind, code EngineLogCode) error {
	recorder := c.logRecorder.Load()
	identity := c.record.Identity
	if recorder == nil {
		return nil
	}
	entry := EngineLogEvent{Event: event, Code: code, InstanceID: identity.InstanceID, Generation: identity.Generation}
	if event == EngineLogStopRequested {
		var err error
		c.logStopOnce.Do(func() { err = recorder.append(entry) })
		return err
	}
	return recorder.append(entry)
}

func (c *engineControl) handleEngineLogStatus(writer http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(writer, req)
		return
	}
	var request engineLogStatusRequest
	if decodeEngineLogStatus(http.MaxBytesReader(writer, req.Body, 2048), &request) != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	mac := request.MAC
	request.MAC = ""
	if request.Version != engineLogStatusVersion || request.Command != "log-status" || len(request.Nonce) != 64 || request.Identity != c.record.Identity || !validMAC(signEngineLogStatus(c.record.Secret, "request", request), mac) {
		http.Error(writer, "unauthorized", http.StatusForbidden)
		return
	}
	result := engineLogStatusResponse{Version: engineLogStatusVersion, Command: "log-status", Nonce: request.Nonce, Identity: c.record.Identity, Health: c.logHealth()}
	result.MAC = signEngineLogStatus(c.record.Secret, "response", result)
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}
