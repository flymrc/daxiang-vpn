package updateverify

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
)

var products = []string{"cli", "desktop-gui", "python-sdk", "hub", "reverse", "android-control"}
var platforms = []string{"windows", "darwin", "linux", "android"}
var architectures = []string{"amd64", "arm64"}
var channels = []string{"stable", "beta", "development"}

func member(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func scope(m Metadata) Scope { return Scope{m.Product, m.Platform, m.Architecture, m.Channel} }

func validScope(s Scope) bool {
	return member(s.Product, products) && member(s.Platform, platforms) && member(s.Architecture, architectures) && member(s.Channel, channels)
}

func exactHex(s string, size int) bool {
	if len(s) != size*2 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func validKeyID(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := range s {
		if !(s[i] >= 'a' && s[i] <= 'z' || s[i] >= '0' && s[i] <= '9' || s[i] == '-') {
			return false
		}
	}
	return true
}

func artifactName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i := range s {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-')) {
			return false
		}
	}
	return true
}

func safePositive(n uint64) bool { return n > 0 && n <= MaxSafeInteger }

func validateMetadata(m Metadata) error {
	v, validVersion := parseVersion(m.Version)
	if !validScope(scope(m)) || !validVersion || m.Channel == "stable" && len(v.pre) != 0 ||
		m.Protocol == 0 || m.Protocol > 1024 || !exactHex(m.SourceCommit, 20) || !artifactName(m.ArtifactName) ||
		m.ArtifactSize <= 0 || m.ArtifactSize > MaxArtifactBytes || !exactHex(m.ArtifactSHA256, 32) ||
		m.IssuedAt <= 0 || m.IssuedAt >= m.ExpiresAt || m.ExpiresAt > MaxTimestamp || m.ExpiresAt-m.IssuedAt > int64(MaxValidity/time.Second) ||
		!safePositive(m.ReleaseSequence) || !safePositive(m.SecurityVersion) || !safePositive(m.SecurityFloor) || m.SecurityFloor > m.SecurityVersion {
		return ErrMetadata
	}
	return nil
}

func validatePolicy(p Policy) (map[string]Key, error) {
	_, validVersion := parseVersion(p.MinVersion)
	if !validScope(p.Scope) || !validVersion || p.MinProtocol == 0 || p.MinProtocol > p.MaxProtocol || p.MaxProtocol > 1024 ||
		!safePositive(p.MinSequence) || !safePositive(p.SecurityFloor) || p.MaxArtifactSize <= 0 || p.MaxArtifactSize > MaxArtifactBytes ||
		p.MaxValidity < time.Second || p.MaxValidity > MaxValidity || p.MaxValidity%time.Second != 0 || len(p.Keys) == 0 || len(p.Keys) > 16 {
		return nil, ErrPolicy
	}
	keys := make(map[string]Key, len(p.Keys))
	publicKeys := make(map[string]bool, len(p.Keys))
	for _, key := range p.Keys {
		pub := string(key.PublicKey)
		if !validKeyID(key.ID) || !allowedPublicKey(key.PublicKey) || key.ValidFrom <= 0 || key.ValidUntil <= key.ValidFrom || key.ValidUntil > MaxTimestamp ||
			key.ValidUntil-key.ValidFrom > 5*366*24*60*60 || !safePositive(key.MinSequence) || !safePositive(key.MaxSequence) || key.MinSequence > key.MaxSequence ||
			publicKeys[pub] {
			return nil, ErrPolicy
		}
		if _, duplicate := keys[key.ID]; duplicate {
			return nil, ErrPolicy
		}
		key.PublicKey = append(ed25519.PublicKey(nil), key.PublicKey...)
		keys[key.ID], publicKeys[pub] = key, true
	}
	return keys, nil
}

func digest(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func validatePrevious(previous *Receipt, p Policy, now int64) error {
	if previous == nil {
		return nil
	}
	raw, err := CanonicalMetadata(previous.Metadata)
	if err != nil || previous.SchemaVersion != SchemaVersion || previous.Status != StagingStatus || !validKeyID(previous.KeyID) ||
		!exactHex(previous.MetadataSHA256, 32) || digest(raw) != previous.MetadataSHA256 || scope(previous.Metadata) != p.Scope ||
		previous.VerifiedAt < previous.Metadata.IssuedAt || previous.VerifiedAt >= previous.Metadata.ExpiresAt || previous.VerifiedAt > now {
		return ErrPrevious
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Verify checks current policy, signature, replay/floors and actual streamed
// artifact bytes, then returns only a staging receipt. No input is mutated.
// Previous must be the last protected receipt for exactly this Scope. Identical
// sequence+metadata+key is idempotent, but current expiry/key/floors and artifact
// bytes are checked again. Older/equivocating sequences and version/floor/security
// downgrades are refused; this first boundary offers no rollback override.
// The caller owns the reader deadline/closure: Context cannot interrupt a reader
// already blocked in Read. At most declared size+1 bytes are consumed.
func Verify(ctx context.Context, rawEnvelope []byte, artifact io.Reader, p Policy, previous *Receipt, now time.Time) (Receipt, error) {
	var empty Receipt
	keys, err := validatePolicy(p)
	if err != nil || ctx == nil || artifact == nil || now.IsZero() || now.Unix() <= 0 || now.Unix() > MaxTimestamp {
		return empty, ErrPolicy
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if len(rawEnvelope) == 0 || len(rawEnvelope) > MaxEnvelopeBytes || strictObject(rawEnvelope, fieldNames(reflect.TypeFor[envelope]())) != nil {
		return empty, ErrMetadata
	}
	var e envelope
	if err := json.Unmarshal(rawEnvelope, &e); err != nil || e.SchemaVersion != SchemaVersion || !validKeyID(e.KeyID) || !exactHex(e.Signature, ed25519.SignatureSize) {
		return empty, ErrMetadata
	}
	m, err := decodeMetadata(e.Metadata)
	if err != nil {
		return empty, err
	}
	if scope(m) != p.Scope || m.Protocol < p.MinProtocol || m.Protocol > p.MaxProtocol || m.ArtifactSize > p.MaxArtifactSize ||
		m.ExpiresAt-m.IssuedAt > int64(p.MaxValidity/time.Second) || m.IssuedAt > now.Unix() || m.ExpiresAt <= now.Unix() {
		return empty, fmt.Errorf("%w: scope, protocol, size or validity refused", ErrMetadata)
	}
	key, exists := keys[e.KeyID]
	if !exists || now.Unix() < key.ValidFrom || now.Unix() >= key.ValidUntil || m.IssuedAt < key.ValidFrom || m.IssuedAt >= key.ValidUntil ||
		m.ReleaseSequence < key.MinSequence || m.ReleaseSequence > key.MaxSequence {
		return empty, ErrSignature
	}
	signature, _ := hex.DecodeString(e.Signature)
	if !ed25519.Verify(key.PublicKey, signingBytes(e.KeyID, e.Metadata), signature) {
		return empty, ErrSignature
	}
	if err := validatePrevious(previous, p, now.Unix()); err != nil {
		return empty, err
	}
	version, _ := parseVersion(m.Version)
	minimum, _ := parseVersion(p.MinVersion)
	if compareVersion(version, minimum) < 0 || m.SecurityFloor < p.SecurityFloor || m.ReleaseSequence < p.MinSequence {
		return empty, ErrRollback
	}
	idempotent := false
	metadataHash := digest(e.Metadata)
	if previous != nil {
		old := previous.Metadata
		if m.ReleaseSequence < old.ReleaseSequence {
			return empty, ErrReplay
		}
		if m.ReleaseSequence == old.ReleaseSequence {
			if metadataHash != previous.MetadataSHA256 || e.KeyID != previous.KeyID {
				return empty, ErrReplay
			}
			idempotent = true
		}
		oldVersion, _ := parseVersion(old.Version)
		if compareVersion(version, oldVersion) < 0 || m.SecurityVersion < old.SecurityVersion || m.SecurityFloor < old.SecurityFloor {
			return empty, ErrRollback
		}
	}
	h := sha256.New()
	n, readErr := io.CopyBuffer(h, io.LimitReader(contextReader{ctx, artifact}, m.ArtifactSize+1), make([]byte, 32<<10))
	if readErr != nil || n != m.ArtifactSize || hex.EncodeToString(h.Sum(nil)) != m.ArtifactSHA256 {
		if ctx.Err() != nil {
			return empty, errors.Join(ErrArtifact, ctx.Err())
		}
		return empty, ErrArtifact
	}
	if err := ctx.Err(); err != nil {
		return empty, errors.Join(ErrArtifact, err)
	}
	return Receipt{SchemaVersion: SchemaVersion, Status: StagingStatus, KeyID: e.KeyID, MetadataSHA256: metadataHash, Metadata: m, VerifiedAt: now.Unix(), Idempotent: idempotent}, nil
}
