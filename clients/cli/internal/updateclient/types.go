// Package updateclient is an explicit offline consumer of signed update
// metadata. It persists staging watermarks, never installs or executes artifacts.
package updateclient

import (
	"errors"
	"zongheng-vpn/shared/updateverify"
)

const (
	Version          = 1
	MaxPolicyBytes   = 16 << 10
	MaxStateBytes    = 64 << 10
	RegistrationName = "update-v1-registration.json"
	StateName        = "update-v1-state.json"
)

var ErrReported = errors.New("offline update result already reported")

type Scope struct {
	Product      string `json:"product"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	Channel      string `json:"channel"`
}

func (s Scope) verifier() updateverify.Scope {
	return updateverify.Scope{Product: s.Product, Platform: s.Platform, Architecture: s.Architecture, Channel: s.Channel}
}

// PolicyDocument is the sole maintenance source for the approved offline JSON.
// Approval binds its exact original UTF-8 bytes, including whitespace.
type PolicyDocument struct {
	SchemaVersion      int         `json:"schema_version"`
	Revision           uint64      `json:"policy_revision"`
	Scope              Scope       `json:"scope"`
	MinVersion         string      `json:"min_version"`
	MinProtocol        uint64      `json:"min_protocol"`
	MaxProtocol        uint64      `json:"max_protocol"`
	MinSequence        uint64      `json:"min_sequence"`
	SecurityFloor      uint64      `json:"security_floor"`
	MaxArtifactSize    int64       `json:"max_artifact_size"`
	MaxValiditySeconds int64       `json:"max_validity_seconds"`
	Keys               []PolicyKey `json:"keys"`
}

type PolicyKey struct {
	ID          string `json:"key_id"`
	PublicKey   string `json:"public_key"`
	ValidFrom   int64  `json:"valid_from"`
	ValidUntil  int64  `json:"valid_until"`
	MinSequence uint64 `json:"min_sequence"`
	MaxSequence uint64 `json:"max_sequence"`
}

// Floors retain approval and staging high-water values independently of the
// currently running version. They never mean an artifact was installed.
type Floors struct {
	MinVersion      string `json:"min_version"`
	MinProtocol     uint64 `json:"min_protocol"`
	MinSequence     uint64 `json:"min_sequence"`
	SecurityFloor   uint64 `json:"security_floor"`
	SecurityVersion uint64 `json:"security_version"`
}

type Receipt struct {
	ContractVersion    int                   `json:"contract_version"`
	Command            string                `json:"command"`
	OK                 bool                  `json:"ok"`
	Outcome            string                `json:"outcome"`
	CommitUnknown      bool                  `json:"commit_unknown"`
	WatermarkCommitted bool                  `json:"watermark_committed"`
	Code               string                `json:"code,omitempty"`
	Scope              *Scope                `json:"scope,omitempty"`
	RegistrationID     string                `json:"registration_id,omitempty"`
	PolicyRevision     uint64                `json:"policy_revision,omitempty"`
	PolicySHA256       string                `json:"policy_sha256,omitempty"`
	Floors             *Floors               `json:"floors,omitempty"`
	Staging            *updateverify.Receipt `json:"staging,omitempty"`
}

type failure struct {
	code    string
	unknown bool
}

func (f *failure) Error() string { return "offline update: " + f.code }
func fail(code string) error     { return &failure{code: code} }

type registration struct {
	SchemaVersion     int    `json:"schema_version"`
	ID                string `json:"registration_id"`
	Scope             Scope  `json:"scope"`
	InitialRevision   uint64 `json:"initial_policy_revision"`
	InitialSHA256     string `json:"initial_policy_sha256"`
	InitialPolicyJSON string `json:"initial_policy_json"`
}

type state struct {
	SchemaVersion  int                   `json:"schema_version"`
	ID             string                `json:"registration_id"`
	Scope          Scope                 `json:"scope"`
	PolicyRevision uint64                `json:"policy_revision"`
	PolicySHA256   string                `json:"policy_sha256"`
	PolicyJSON     string                `json:"policy_json"`
	Floors         Floors                `json:"floors"`
	HasPrevious    bool                  `json:"has_previous"`
	Previous       *updateverify.Receipt `json:"previous,omitempty"`
}
