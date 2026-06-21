package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRotateIPBusyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(RotateIPResult{
			Status:            "busy",
			Egress:            "jp-android-01",
			DownSeconds:       8,
			Message:           "换 IP 正在进行中，请稍后再试",
			RetryAfterSeconds: 42,
		})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	res, err := RotateIP("ZH-OK", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "busy" {
		t.Fatalf("status = %q", res.Status)
	}
	if res.RetryAfterSeconds != 42 {
		t.Fatalf("retry_after_seconds = %d", res.RetryAfterSeconds)
	}
}

func TestRotateIPTriggeredResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RotateIPResult{
			Status:      "triggered",
			Egress:      "jp-android-01",
			DownSeconds: 8,
		})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	res, err := RotateIP("ZH-OK", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "triggered" {
		t.Fatalf("status = %q", res.Status)
	}
}

func TestRotateIPInvalidDownSecondsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid_down_seconds"})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	_, err := RotateIP("ZH-OK", 61)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "断网秒数必须在 1 到 60 秒之间" {
		t.Fatalf("error = %q", err.Error())
	}
}
