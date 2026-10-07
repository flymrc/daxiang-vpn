package updateverify

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	m        Metadata
	p        Policy
	key      ed25519.PrivateKey
	artifact []byte
	now      time.Time
}

func newFixture() fixture {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{23}, ed25519.SeedSize))
	artifact := []byte("controlled single-artifact bytes")
	now := time.Unix(1800000000, 0)
	hash := sha256.Sum256(artifact)
	m := Metadata{Product: "cli", Platform: "windows", Architecture: "amd64", Channel: "stable", Version: "1.2.3", Protocol: 2, SourceCommit: strings.Repeat("a", 40), ArtifactName: "zhvpn.exe", ArtifactSize: int64(len(artifact)), ArtifactSHA256: hex.EncodeToString(hash[:]), IssuedAt: now.Unix() - 10, ExpiresAt: now.Unix() + 300, ReleaseSequence: 10, SecurityVersion: 5, SecurityFloor: 3}
	p := Policy{Scope: scope(m), MinVersion: "1.0.0", MinProtocol: 2, MaxProtocol: 2, MinSequence: 1, SecurityFloor: 2, MaxArtifactSize: 1 << 20, MaxValidity: time.Hour, Keys: []Key{{ID: "release-a", PublicKey: key.Public().(ed25519.PublicKey), ValidFrom: now.Unix() - 3600, ValidUntil: now.Unix() + 3600, MinSequence: 1, MaxSequence: 100}}}
	return fixture{m, p, key, artifact, now}
}

func signedRaw(t *testing.T, raw []byte, keyID string, key ed25519.PrivateKey) []byte {
	t.Helper()
	sig := ed25519.Sign(key, signingBytes(keyID, raw))
	encodedKey, _ := json.Marshal(keyID)
	// Preserve exact bytes; json.Marshal compacts RawMessage whitespace fixtures.
	out := append([]byte(`{"schema_version":1,"key_id":`), encodedKey...)
	out = append(out, []byte(`,"metadata":`)...)
	out = append(out, raw...)
	return append(out, []byte(`,"signature":"`+hex.EncodeToString(sig)+`"}`)...)
}

func signed(t *testing.T, m Metadata, keyID string, key ed25519.PrivateKey) []byte {
	t.Helper()
	// Intentionally bypass the publisher syntax helper for invalid signed fixtures.
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return signedRaw(t, raw, keyID, key)
}

func accept(t *testing.T, f fixture, previous *Receipt) Receipt {
	t.Helper()
	r, err := Verify(context.Background(), signed(t, f.m, "release-a", f.key), bytes.NewReader(f.artifact), f.p, previous, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCanonicalSchemaAndSigningFrame(t *testing.T) {
	schema, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile("metadata-v1.schema.json")
	if err != nil || !bytes.Equal(checkedIn, schema) {
		t.Fatal("canonical schema drift", err)
	}
	second, _ := Schema()
	if !bytes.Equal(schema, second) {
		t.Fatal("schema generation not deterministic")
	}
	f := newFixture()
	raw, err := CanonicalMetadata(f.m)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := SigningMessage("release-a", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(msg, []byte("zhvpn/update-metadata/v1\x00")) {
		t.Fatal("domain changed")
	}
	rest := msg[len(signingDomain):]
	if binary.BigEndian.Uint32(rest[:4]) != uint32(len("release-a")) || string(rest[4:4+len("release-a")]) != "release-a" {
		t.Fatal("key frame changed")
	}
	rest = rest[4+len("release-a"):]
	if binary.BigEndian.Uint32(rest[:4]) != uint32(len(raw)) || !bytes.Equal(rest[4:], raw) {
		t.Fatal("metadata frame changed")
	}
	var projection map[string]any
	if json.Unmarshal(schema, &projection) != nil {
		t.Fatal("schema invalid")
	}
	definition := projection["$defs"].(map[string]any)["metadata"].(map[string]any)
	if len(definition["properties"].(map[string]any)) != reflect.TypeFor[Metadata]().NumField() {
		t.Fatal("field missing from schema")
	}
}

func TestFirstStagingNewReleaseAndExactIdempotentRevalidation(t *testing.T) {
	f := newFixture()
	before, _ := json.Marshal(f.p)
	first := accept(t, f, nil)
	if first.Status != StagingStatus || first.Idempotent || first.Metadata != f.m || first.VerifiedAt != f.now.Unix() {
		t.Fatal("invalid staging result")
	}
	retry := accept(t, f, &first)
	if !retry.Idempotent || retry.MetadataSHA256 != first.MetadataSHA256 {
		t.Fatal("same release did not revalidate idempotently")
	}
	f.m.ReleaseSequence++
	f.m.Version = "1.2.4"
	f.m.SecurityVersion++
	f.m.SecurityFloor++
	newer := accept(t, f, &first)
	if newer.Idempotent || newer.Metadata.ReleaseSequence != first.Metadata.ReleaseSequence+1 {
		t.Fatal("new release misclassified")
	}
	after, _ := json.Marshal(f.p)
	if !bytes.Equal(before, after) {
		t.Fatal("verifier mutated trusted policy")
	}
}

func TestScopeValidityAndResourceBindingsRefuseBeforeArtifactRead(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fixture)
		want   error
	}{
		{"product", func(f *fixture) { f.m.Product = "reverse" }, ErrMetadata},
		{"platform", func(f *fixture) { f.m.Platform = "darwin" }, ErrMetadata},
		{"arch", func(f *fixture) { f.m.Architecture = "arm64" }, ErrMetadata},
		{"channel", func(f *fixture) { f.m.Channel = "beta" }, ErrMetadata},
		{"protocol", func(f *fixture) { f.m.Protocol = 3 }, ErrMetadata},
		{"commit-size", func(f *fixture) { f.m.SourceCommit = "abcdef" }, ErrMetadata},
		{"commit-upper", func(f *fixture) { f.m.SourceCommit = strings.Repeat("A", 40) }, ErrMetadata},
		{"path-name", func(f *fixture) { f.m.ArtifactName = "../zhvpn.exe" }, ErrMetadata},
		{"unicode-name", func(f *fixture) { f.m.ArtifactName = "纵横.exe" }, ErrMetadata},
		{"expired", func(f *fixture) { f.m.ExpiresAt = f.now.Unix() }, ErrMetadata},
		{"future", func(f *fixture) { f.m.IssuedAt = f.now.Unix() + 1 }, ErrMetadata},
		{"policy-ttl", func(f *fixture) { f.m.ExpiresAt = f.m.IssuedAt + 7200 }, ErrMetadata},
		{"absolute-ttl", func(f *fixture) { f.m.ExpiresAt = f.m.IssuedAt + int64(MaxValidity/time.Second) + 1 }, ErrMetadata},
		{"oversize", func(f *fixture) { f.m.ArtifactSize = f.p.MaxArtifactSize + 1 }, ErrMetadata},
		{"zero-size", func(f *fixture) { f.m.ArtifactSize = 0 }, ErrMetadata},
		{"unsafe-sequence", func(f *fixture) { f.m.ReleaseSequence = MaxSafeInteger + 1 }, ErrMetadata},
		{"floor-above-version", func(f *fixture) { f.m.SecurityFloor = f.m.SecurityVersion + 1 }, ErrMetadata},
		{"stable-prerelease", func(f *fixture) { f.m.Version = "1.2.3-rc.1" }, ErrMetadata},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.change(&f)
			reader := &countReader{r: bytes.NewReader(f.artifact)}
			_, err := Verify(context.Background(), signed(t, f.m, "release-a", f.key), reader, f.p, nil, f.now)
			if !errors.Is(err, tc.want) || reader.n != 0 {
				t.Fatalf("binding refusal/read=%d: %v", reader.n, err)
			}
		})
	}
}

func TestStrictJSONRefusesUnknownDuplicateNullCasingAndNoncanonicalValues(t *testing.T) {
	f := newFixture()
	raw, _ := CanonicalMetadata(f.m)
	base := signed(t, f.m, "release-a", f.key)
	bad := map[string][]byte{
		"envelope-case":      bytes.Replace(base, []byte(`"schema_version"`), []byte(`"Schema_version"`), 1),
		"envelope-duplicate": append([]byte(`{"schema_version":1,`), base[1:]...),
		"envelope-unknown":   append([]byte(`{"extra":1,`), base[1:]...),
		"trailing":           append(append([]byte(nil), base...), []byte(`{}`)...),
		"oversized":          bytes.Repeat([]byte{' '}, MaxEnvelopeBytes+1),
	}
	for name, value := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(context.Background(), value, bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrMetadata) {
				t.Fatal(err)
			}
		})
	}
	for _, field := range fieldNames(reflect.TypeFor[Metadata]()) {
		t.Run("null-"+field, func(t *testing.T) {
			var values map[string]json.RawMessage
			_ = json.Unmarshal(raw, &values)
			values[field] = json.RawMessage("null")
			changed, _ := json.Marshal(values)
			if _, err := Verify(context.Background(), signedRaw(t, changed, "release-a", f.key), bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrMetadata) {
				t.Fatal(err)
			}
		})
	}
	for name, changed := range map[string][]byte{
		"missing":           bytes.Replace(raw, []byte(`"protocol":2,`), nil, 1),
		"case":              bytes.Replace(raw, []byte(`"protocol"`), []byte(`"Protocol"`), 1),
		"unknown":           append([]byte(`{"unknown":1,`), raw[1:]...),
		"duplicate":         append([]byte(`{"protocol":2,`), raw[1:]...),
		"escaped-duplicate": append([]byte(`{"\u0070rotocol":2,`), raw[1:]...),
		"whitespace":        bytes.Replace(raw, []byte(`"product":`), []byte(`"product": `), 1),
		"fraction":          bytes.Replace(raw, []byte(`"protocol":2`), []byte(`"protocol":2.0`), 1),
		"exponent":          bytes.Replace(raw, []byte(`"protocol":2`), []byte(`"protocol":2e0`), 1),
		"negative":          bytes.Replace(raw, []byte(`"release_sequence":10`), []byte(`"release_sequence":-1`), 1),
		"uint-overflow":     bytes.Replace(raw, []byte(`"release_sequence":10`), []byte(`"release_sequence":18446744073709551616`), 1),
	} {
		t.Run("metadata-"+name, func(t *testing.T) {
			if _, err := Verify(context.Background(), signedRaw(t, changed, "release-a", f.key), bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrMetadata) {
				t.Fatal(err)
			}
		})
	}
	var envFields map[string]json.RawMessage
	_ = json.Unmarshal(base, &envFields)
	for _, field := range fieldNames(reflect.TypeFor[envelope]()) {
		t.Run("envelope-null-"+field, func(t *testing.T) {
			copy := map[string]json.RawMessage{}
			for k, v := range envFields {
				copy[k] = v
			}
			copy[field] = json.RawMessage("null")
			changed, _ := json.Marshal(copy)
			if _, err := Verify(context.Background(), changed, bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrMetadata) {
				t.Fatal(err)
			}
		})
	}
}

func TestSignatureDomainTamperingAndKeyAuthorizationWindows(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fixture)
		keyID  string
	}{
		{"unknown", func(*fixture) {}, "missing"},
		{"removed", func(f *fixture) { f.p.Keys[0].ID = "release-b" }, "release-a"},
		{"key-expired", func(f *fixture) { f.p.Keys[0].ValidUntil = f.now.Unix() }, "release-a"},
		{"key-future", func(f *fixture) { f.p.Keys[0].ValidFrom = f.now.Unix() + 1 }, "release-a"},
		{"issued-before-key", func(f *fixture) { f.p.Keys[0].ValidFrom = f.m.IssuedAt + 1 }, "release-a"},
		{"sequence-below-key", func(f *fixture) { f.p.Keys[0].MinSequence = f.m.ReleaseSequence + 1 }, "release-a"},
		{"sequence-above-key", func(f *fixture) { f.p.Keys[0].MaxSequence = f.m.ReleaseSequence - 1 }, "release-a"},
		{"wrong-public-key", func(f *fixture) {
			f.p.Keys[0].PublicKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{44}, 32)).Public().(ed25519.PublicKey)
		}, "release-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.change(&f)
			if _, err := Verify(context.Background(), signed(t, f.m, tc.keyID, f.key), bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrSignature) {
				t.Fatal(err)
			}
		})
	}
	f := newFixture()
	raw, _ := CanonicalMetadata(f.m)
	sig := ed25519.Sign(f.key, append([]byte("different-domain\x00"), raw...))
	encoded, _ := json.Marshal(envelope{SchemaVersion, "release-a", raw, hex.EncodeToString(sig)})
	if _, err := Verify(context.Background(), encoded, bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrSignature) {
		t.Fatal("domain confusion", err)
	}
	var e envelope
	_ = json.Unmarshal(signed(t, f.m, "release-a", f.key), &e)
	e.Metadata = bytes.Replace(e.Metadata, []byte(`"version":"1.2.3"`), []byte(`"version":"1.2.4"`), 1)
	encoded, _ = json.Marshal(e)
	if _, err := Verify(context.Background(), encoded, bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrSignature) {
		t.Fatal("tamper accepted", err)
	}
}

func TestReplayRollbackAndCurrentPolicyStillApplyToIdempotentReceipt(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fixture)
		want   error
	}{
		{"older-sequence", func(f *fixture) { f.m.ReleaseSequence-- }, ErrReplay},
		{"same-sequence-version", func(f *fixture) { f.m.Version = "1.2.4" }, ErrReplay},
		{"same-sequence-commit", func(f *fixture) { f.m.SourceCommit = strings.Repeat("b", 40) }, ErrReplay},
		{"version-rollback", func(f *fixture) { f.m.ReleaseSequence++; f.m.Version = "1.2.2" }, ErrRollback},
		{"security-rollback", func(f *fixture) { f.m.ReleaseSequence++; f.m.SecurityVersion-- }, ErrRollback},
		{"floor-rollback", func(f *fixture) { f.m.ReleaseSequence++; f.m.SecurityFloor-- }, ErrRollback},
		{"new-policy-sequence", func(f *fixture) { f.p.MinSequence = f.m.ReleaseSequence + 1 }, ErrRollback},
		{"new-policy-version", func(f *fixture) { f.p.MinVersion = "1.3.0" }, ErrRollback},
		{"new-policy-floor", func(f *fixture) { f.p.SecurityFloor = f.m.SecurityFloor + 1 }, ErrRollback},
		{"idempotent-expired", func(f *fixture) { f.now = time.Unix(f.m.ExpiresAt, 0) }, ErrMetadata},
		{"idempotent-key-removed", func(f *fixture) { f.p.Keys[0].ID = "release-b" }, ErrSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			previous := accept(t, f, nil)
			tc.change(&f)
			if _, err := Verify(context.Background(), signed(t, f.m, "release-a", f.key), bytes.NewReader(f.artifact), f.p, &previous, f.now); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	f := newFixture()
	previous := accept(t, f, nil)
	rotated := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{45}, 32))
	f.p.Keys = append(f.p.Keys, Key{ID: "release-b", PublicKey: rotated.Public().(ed25519.PublicKey), ValidFrom: f.now.Unix() - 60, ValidUntil: f.now.Unix() + 600, MinSequence: 11, MaxSequence: 100})
	f.m.ReleaseSequence++
	f.m.Version = "1.3.0"
	if _, err := Verify(context.Background(), signed(t, f.m, "release-b", rotated), bytes.NewReader(f.artifact), f.p, &previous, f.now); err != nil {
		t.Fatal("trusted rotation refused", err)
	}
	f.m.ReleaseSequence--
	if _, err := Verify(context.Background(), signed(t, f.m, "release-b", rotated), bytes.NewReader(f.artifact), f.p, &previous, f.now); !errors.Is(err, ErrSignature) {
		t.Fatal("rotation sequence boundary missing", err)
	}
}

func TestInvalidPolicyAndForeignOrIncoherentPreviousRefuse(t *testing.T) {
	policies := map[string]func(*Policy){
		"empty-scope": func(p *Policy) { p.Scope.Product = "" }, "invalid-min-version": func(p *Policy) { p.MinVersion = "1.0" },
		"zero-protocol": func(p *Policy) { p.MinProtocol = 0 }, "inverted-protocol": func(p *Policy) { p.MaxProtocol = 1 },
		"zero-sequence": func(p *Policy) { p.MinSequence = 0 }, "unsafe-floor": func(p *Policy) { p.SecurityFloor = MaxSafeInteger + 1 },
		"unbounded-size": func(p *Policy) { p.MaxArtifactSize = MaxArtifactBytes + 1 }, "fraction-ttl": func(p *Policy) { p.MaxValidity = time.Second + time.Nanosecond },
		"unbounded-ttl": func(p *Policy) { p.MaxValidity = MaxValidity + time.Second }, "no-key": func(p *Policy) { p.Keys = nil },
		"short-key": func(p *Policy) { p.Keys[0].PublicKey = []byte{1} }, "key-duplicate": func(p *Policy) { p.Keys = append(p.Keys, p.Keys[0]) },
		"public-alias": func(p *Policy) { k := p.Keys[0]; k.ID = "release-b"; p.Keys = append(p.Keys, k) },
		"key-window":   func(p *Policy) { p.Keys[0].ValidUntil = p.Keys[0].ValidFrom }, "key-sequence": func(p *Policy) { p.Keys[0].MinSequence = 101 },
	}
	for name, change := range policies {
		t.Run("policy-"+name, func(t *testing.T) {
			f := newFixture()
			change(&f.p)
			if _, err := Verify(context.Background(), signed(t, f.m, "release-a", f.key), bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrPolicy) {
				t.Fatal(err)
			}
		})
	}
	previousChanges := map[string]func(*Receipt){
		"foreign-scope": func(r *Receipt) {
			r.Metadata.Product = "reverse"
			raw, _ := CanonicalMetadata(r.Metadata)
			r.MetadataSHA256 = digest(raw)
		},
		"digest": func(r *Receipt) { r.MetadataSHA256 = strings.Repeat("f", 64) }, "installed": func(r *Receipt) { r.Status = "installed" },
		"schema": func(r *Receipt) { r.SchemaVersion++ }, "future-verification": func(r *Receipt) { r.VerifiedAt++ }, "before-issued": func(r *Receipt) { r.VerifiedAt = r.Metadata.IssuedAt - 1 },
	}
	for name, change := range previousChanges {
		t.Run("previous-"+name, func(t *testing.T) {
			f := newFixture()
			r := accept(t, f, nil)
			change(&r)
			if _, err := Verify(context.Background(), signed(t, f.m, "release-a", f.key), bytes.NewReader(f.artifact), f.p, &r, f.now); !errors.Is(err, ErrPrevious) {
				t.Fatal(err)
			}
		})
	}
}

type countReader struct {
	r io.Reader
	n int
}

func (r *countReader) Read(p []byte) (int, error) { n, err := r.r.Read(p); r.n += n; return n, err }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("secret reader error text") }

type cancelReader struct{ cancel context.CancelFunc }

func (r cancelReader) Read(p []byte) (int, error) { r.cancel(); p[0] = 1; return 1, nil }

func TestActualArtifactLengthHashReadBudgetFailuresAndCancellation(t *testing.T) {
	f := newFixture()
	env := signed(t, f.m, "release-a", f.key)
	for name, reader := range map[string]io.Reader{"short": bytes.NewReader(f.artifact[:len(f.artifact)-1]), "long": bytes.NewReader(append(append([]byte(nil), f.artifact...), 1)), "hash": bytes.NewReader(bytes.Repeat([]byte{1}, len(f.artifact))), "read-failure": failingReader{}} {
		t.Run(name, func(t *testing.T) {
			_, err := Verify(context.Background(), env, reader, f.p, nil, f.now)
			if !errors.Is(err, ErrArtifact) || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
	r := &countReader{r: bytes.NewReader(bytes.Repeat([]byte{1}, 100000))}
	_, err := Verify(context.Background(), env, r, f.p, nil, f.now)
	if !errors.Is(err, ErrArtifact) || int64(r.n) != f.m.ArtifactSize+1 {
		t.Fatal("read budget missing", r.n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = Verify(ctx, env, cancelReader{cancel}, f.p, nil, f.now)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrArtifact) {
		t.Fatal("cancellation lost", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = Verify(ctx, env, bytes.NewReader(f.artifact), f.p, nil, f.now)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("already canceled context accepted", err)
	}
	if _, err = Verify(nil, env, bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	if _, err = Verify(context.Background(), env, nil, f.p, nil, f.now); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
}

func TestConcurrentVerificationUsesNoGlobalState(t *testing.T) {
	f := newFixture()
	previous := accept(t, f, nil)
	env := signed(t, f.m, "release-a", f.key)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := Verify(context.Background(), env, bytes.NewReader(f.artifact), f.p, &previous, f.now)
			if err != nil || !r.Idempotent {
				t.Error("parallel revalidation failed", err)
			}
		}()
	}
	wg.Wait()
}

func TestWeakPinnedPublicKeyCannotTurnTrivialSignatureIntoAuthority(t *testing.T) {
	f := newFixture()
	var identity [32]byte
	identity[0] = 1
	var trivial [ed25519.SignatureSize]byte
	trivial[0] = 1
	raw, _ := CanonicalMetadata(f.m)
	message, _ := SigningMessage("release-a", raw)
	// This locks the exact configuration seam: a trusted ring still needs
	// structural key checks; the primitive alone accepts this identity key.
	if !ed25519.Verify(identity[:], message, trivial[:]) {
		t.Fatal("Go primitive behavior changed; review weak-key fixture")
	}
	f.p.Keys[0].PublicKey = identity[:]
	encoded, _ := json.Marshal(envelope{SchemaVersion, "release-a", raw, hex.EncodeToString(trivial[:])})
	if _, err := Verify(context.Background(), encoded, bytes.NewReader(f.artifact), f.p, nil, f.now); !errors.Is(err, ErrPolicy) {
		t.Fatal("weak pinned key granted staging", err)
	}
	points := []string{
		strings.Repeat("00", 32), "01" + strings.Repeat("00", 31),
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
		"ec" + strings.Repeat("ff", 30) + "7f", "ed" + strings.Repeat("ff", 30) + "7f", "ee" + strings.Repeat("ff", 30) + "7f",
	}
	for _, encoded := range points {
		point, _ := hex.DecodeString(encoded)
		if allowedPublicKey(point) {
			t.Fatal("small/noncanonical key accepted")
		}
		point[31] |= 0x80
		if allowedPublicKey(point) {
			t.Fatal("sign-bit alias accepted")
		}
	}
	if !allowedPublicKey(newFixture().p.Keys[0].PublicKey) {
		t.Fatal("proper generated key refused")
	}
}

func TestSemVerPrecedenceAndOverflowCannotBecomeRollbackBypass(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0"}
	for i := 1; i < len(ordered); i++ {
		a, aok := parseVersion(ordered[i-1])
		b, bok := parseVersion(ordered[i])
		if !aok || !bok || compareVersion(a, b) >= 0 || compareVersion(b, a) <= 0 {
			t.Fatal("SemVer order wrong", ordered[i-1], ordered[i])
		}
	}
	for _, bad := range []string{"1.2", "v1.2.3", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-", "1.2.3+", "1.2.3-a..b", "1.2.3+build..x", "4294967296.0.0", "18446744073709551616.0.0", "1.2.3-汉字", "1.2.3+invalid_", "1.2.3-" + strings.Repeat("9", 65)} {
		if _, ok := parseVersion(bad); ok {
			t.Fatal("invalid SemVer accepted", bad)
		}
	}
	a, aok := parseVersion("4294967295.0.0")
	b, bok := parseVersion("4294967294.999.999")
	if !aok || !bok || compareVersion(a, b) <= 0 {
		t.Fatal("bounded core compare wrong")
	}
	a, aok = parseVersion("1.0.0-" + strings.Repeat("9", 63))
	b, bok = parseVersion("1.0.0-1" + strings.Repeat("0", 63))
	if !aok || !bok || compareVersion(a, b) >= 0 {
		t.Fatal("large numeric prerelease overflow")
	}
	a, _ = parseVersion("1.2.3+build.01")
	b, _ = parseVersion("1.2.3+build.99")
	if compareVersion(a, b) != 0 {
		t.Fatal("build metadata affected precedence")
	}
	f := newFixture()
	f.m.Channel = "beta"
	f.p.Scope.Channel = "beta"
	f.m.Version = "1.2.3-beta.11"
	previous := accept(t, f, nil)
	f.m.Version = "1.2.3-beta.2"
	f.m.ReleaseSequence++
	if _, err := Verify(context.Background(), signed(t, f.m, "release-a", f.key), bytes.NewReader(f.artifact), f.p, &previous, f.now); !errors.Is(err, ErrRollback) {
		t.Fatal("prerelease downgrade accepted", err)
	}
}
