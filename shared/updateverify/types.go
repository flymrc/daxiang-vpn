// Package updateverify verifies one signed artifact for staging. It never opens
// paths, fetches a network resource, installs, or persists anti-replay authority.
// Callers must supply protected policy/previous receipts and a bounded reader.
package updateverify

import (
	"crypto/ed25519"
	"errors"
	"time"
)

const (
	SchemaVersion    = 1
	MaxMetadataBytes = 16 << 10
	MaxEnvelopeBytes = 24 << 10
	MaxArtifactBytes = int64(8 << 30)
	MaxSafeInteger   = uint64(9007199254740991)
	MaxValidity      = 30 * 24 * time.Hour
	MaxTimestamp     = int64(253402300799)
	StagingStatus    = "verified_for_staging"
)

var (
	ErrPolicy    = errors.New("updateverify: invalid trusted policy")
	ErrMetadata  = errors.New("updateverify: invalid metadata")
	ErrSignature = errors.New("updateverify: signature or key authorization refused")
	ErrPrevious  = errors.New("updateverify: invalid previous receipt")
	ErrReplay    = errors.New("updateverify: replay or equivocation refused")
	ErrRollback  = errors.New("updateverify: rollback refused")
	ErrArtifact  = errors.New("updateverify: artifact verification failed")
)

// Metadata is the canonical maintenance source and field order for signed JSON.
// One envelope describes exactly one artifact. Identifiers and hashes are public.
type Metadata struct {
	Product         string `json:"product"`
	Platform        string `json:"platform"`
	Architecture    string `json:"architecture"`
	Channel         string `json:"channel"`
	Version         string `json:"version"`
	Protocol        uint64 `json:"protocol"`
	SourceCommit    string `json:"source_commit"`
	ArtifactName    string `json:"artifact_name"`
	ArtifactSize    int64  `json:"artifact_size"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	IssuedAt        int64  `json:"issued_at"`
	ExpiresAt       int64  `json:"expires_at"`
	ReleaseSequence uint64 `json:"release_sequence"`
	SecurityVersion uint64 `json:"security_version"`
	SecurityFloor   uint64 `json:"security_floor"`
}

type Scope struct {
	Product, Platform, Architecture, Channel string
}

// Key is pinned by the caller. Metadata cannot add/rotate keys. Both issuance
// and verification must fall in [ValidFrom,ValidUntil); sequences are inclusive.
type Key struct {
	ID                       string
	PublicKey                ed25519.PublicKey
	ValidFrom, ValidUntil    int64
	MinSequence, MaxSequence uint64
}

// Policy must come from a trusted local boundary, never from the candidate.
// Initial nil Previous is permitted only for first enrollment with trusted
// floors. Dropping a persisted Previous loses cross-call replay protection.
type Policy struct {
	Scope           Scope
	MinVersion      string
	MinProtocol     uint64
	MaxProtocol     uint64
	MinSequence     uint64
	SecurityFloor   uint64
	MaxArtifactSize int64
	MaxValidity     time.Duration
	Keys            []Key
}

// Receipt is only a verified staging result. It proves neither installation nor
// healthy runtime/signing release. The caller must atomically persist it under
// its own integrity/access policy before advancing update authority.
type Receipt struct {
	SchemaVersion  int      `json:"schema_version"`
	Status         string   `json:"status"`
	KeyID          string   `json:"key_id"`
	MetadataSHA256 string   `json:"metadata_sha256"`
	Metadata       Metadata `json:"metadata"`
	VerifiedAt     int64    `json:"verified_at"`
	Idempotent     bool     `json:"idempotent"`
}
