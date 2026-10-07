package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	secureTCPTransport         = "tcp-tls"
	secureTCPALPN              = "zhreverse/2"
	secureTCPHello             = "ZHREV2\n"
	secureTCPAck               = "OK ZHREV2\n"
	securePolicyVersion        = 1
	secureRecheckInterval      = time.Second
	secureHandshakeTimeout     = 10 * time.Second
	secureMaxOverlap           = 15 * time.Minute
	secureMaxPolicyBytes       = 1 << 20
	secureMaxSessionsPerEgress = 2
)

// secureRegistry is the operator-owned authorization source, not a CA-wide
// grant. Leaf fingerprints identify independently replaceable credentials.
type secureRegistry struct {
	SchemaVersion int            `json:"schema_version"`
	Generation    uint64         `json:"generation"`
	HubID         string         `json:"hub_id"`
	Egresses      []secureEgress `json:"egresses"`
}

type secureEgress struct {
	EgressID    string             `json:"egress_id"`
	State       string             `json:"state"`
	Credentials []secureCredential `json:"credentials"`
}

type secureCredential struct {
	CredentialID string    `json:"credential_id"`
	LeafSHA256   string    `json:"leaf_sha256"`
	State        string    `json:"state"`
	NotBefore    time.Time `json:"not_before"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type securePolicyWatermark struct {
	SchemaVersion int    `json:"schema_version"`
	Generation    uint64 `json:"generation"`
	SHA256        string `json:"sha256"`
}

type secureSessionIdentity struct {
	ProtocolVersion int       `json:"protocol_version"`
	Transport       string    `json:"transport"`
	EgressID        string    `json:"egress_id"`
	CredentialID    string    `json:"credential_id"`
	LeafSHA256      string    `json:"leaf_sha256"`
	ExpiresAt       time.Time `json:"credential_expires_at_on_admission"`
}

func validSecureID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validateSecureServerOptions(o serverOptions) error {
	if o.Transport != secureTCPTransport {
		return nil
	}
	if o.Token != "" || o.TokenFile != "" {
		return errors.New("tcp-tls forbids shared token configuration")
	}
	if o.TLSCAFile == "" || o.TLSCertFile == "" || o.TLSKeyFile == "" || o.EgressRegistryFile == "" || !validSecureID(o.HubID) {
		return errors.New("tcp-tls requires tls-ca-file, tls-cert-file, tls-key-file, egress-registry-file and a valid hub-id")
	}
	return nil
}

func validateSecureClientOptions(o clientOptions) error {
	if o.Transport != secureTCPTransport {
		return nil
	}
	if o.Token != "" || o.TokenFile != "" || o.InsecureSkipVerify || o.ServerCertSHA256 != "" {
		return errors.New("tcp-tls forbids shared tokens, insecure verification and QUIC pin overrides")
	}
	if o.TLSCAFile == "" || o.TLSCertFile == "" || o.TLSKeyFile == "" || strings.TrimSpace(o.TLSServerName) == "" || !validSecureID(o.HubID) || !validSecureID(o.EgressID) {
		return errors.New("tcp-tls requires tls-ca-file, tls-cert-file, tls-key-file, tls-server-name and valid hub-id/egress-id")
	}
	if o.Connections < 1 || o.Connections > secureMaxSessionsPerEgress {
		return errors.New("tcp-tls connections must be between 1 and 2")
	}
	return nil
}

func decodeSecureJSON(data []byte, out any) error {
	// encoding/json otherwise accepts duplicate members with last-value wins.
	// Authorization files reject that ambiguity at every object depth.
	check := json.NewDecoder(bytes.NewReader(data))
	if err := rejectSecureDuplicateKeys(check, reflect.TypeOf(out)); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("trailing policy JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("trailing policy JSON")
	}
	return nil
}

func rejectSecureDuplicateKeys(dec *json.Decoder, schema reflect.Type) error {
	if schema == nil {
		return errors.New("policy JSON requires a typed schema")
	}
	for schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		if schema.Kind() != reflect.Struct {
			return errors.New("policy JSON object does not match schema")
		}
		fields := make(map[string]reflect.Type)
		for i := 0; i < schema.NumField(); i++ {
			field := schema.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.IsExported() && name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		keys := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			fieldSchema, known := fields[name]
			if !ok || !known {
				return errors.New("unknown or non-canonical policy JSON field")
			}
			if keys[name] {
				return errors.New("duplicate policy JSON key")
			}
			keys[name] = true
			if err := rejectSecureDuplicateKeys(dec, fieldSchema); err != nil {
				return err
			}
		}
	case '[':
		if schema.Kind() != reflect.Slice && schema.Kind() != reflect.Array {
			return errors.New("policy JSON array does not match schema")
		}
		for dec.More() {
			if err := rejectSecureDuplicateKeys(dec, schema.Elem()); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid policy JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func readSecureFile(path string, limit int64) ([]byte, error) {
	return readSecureFileWithSecret(path, limit, false)
}

func readSecureFileWithSecret(path string, limit int64, private bool) ([]byte, error) {
	if err := validateSecureDirectoryPath(path); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("secure file must be a regular file, not a symlink")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		opened, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if !os.SameFile(info, opened) {
			f.Close()
			continue
		}
		if err := validateSecureFileHandle(f); err != nil {
			f.Close()
			return nil, err
		}
		if err := validateSecureAuthorityHandle(f); err != nil {
			f.Close()
			return nil, err
		}
		if private {
			if err := validateSecurePrivateKeyHandle(f); err != nil {
				f.Close()
				return nil, err
			}
		}
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errors.New("secure file exceeds size limit")
		}
		return data, nil
	}
	return nil, errors.New("secure file repeatedly changed while opening")
}

func validateSecureRegistry(p *secureRegistry, hubID string) error {
	if p.SchemaVersion != securePolicyVersion || p.Generation == 0 || p.HubID != hubID {
		return errors.New("invalid registry schema, generation or Hub identity")
	}
	if p.Egresses == nil || len(p.Egresses) > 128 {
		return errors.New("registry exceeds 128 egress identities")
	}
	ids := map[string]bool{}
	fingerprints := map[string]bool{}
	for _, e := range p.Egresses {
		if !validSecureID(e.EgressID) || ids[e.EgressID] || (e.State != "active" && e.State != "revoked") {
			return errors.New("invalid or duplicate egress")
		}
		ids[e.EgressID] = true
		if e.Credentials == nil || len(e.Credentials) > 2 || (e.State == "active" && len(e.Credentials) == 0) {
			return errors.New("active egress requires one or two credentials")
		}
		credentialIDs := map[string]bool{}
		var active []secureCredential
		for _, c := range e.Credentials {
			fp, err := hex.DecodeString(c.LeafSHA256)
			if err != nil || len(fp) != sha256.Size || strings.ToLower(c.LeafSHA256) != c.LeafSHA256 || fingerprints[c.LeafSHA256] {
				return errors.New("invalid or duplicate leaf fingerprint")
			}
			fingerprints[c.LeafSHA256] = true
			if !validSecureID(c.CredentialID) || credentialIDs[c.CredentialID] || (c.State != "active" && c.State != "revoked") || c.NotBefore.IsZero() || !c.ExpiresAt.After(c.NotBefore) {
				return errors.New("invalid credential identity, state or validity")
			}
			credentialIDs[c.CredentialID] = true
			if c.State == "active" {
				active = append(active, c)
			}
		}
		if len(active) == 2 {
			start := active[0].NotBefore
			if active[1].NotBefore.After(start) {
				start = active[1].NotBefore
			}
			end := active[0].ExpiresAt
			if active[1].ExpiresAt.Before(end) {
				end = active[1].ExpiresAt
			}
			if end.Sub(start) > secureMaxOverlap {
				return errors.New("credential overlap exceeds 15 minutes")
			}
		}
	}
	return nil
}

func loadSecureRegistry(path, hubID string) (*secureRegistry, string, error) {
	data, err := readSecureFile(path, secureMaxPolicyBytes)
	if err != nil {
		return nil, "", err
	}
	var p secureRegistry
	if err := decodeSecureJSON(data, &p); err != nil {
		return nil, "", err
	}
	if err := validateSecureRegistry(&p, hubID); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return &p, hex.EncodeToString(sum[:]), nil
}

// The caller holds the registry lifetime file lock. The watermark is committed
// before authorization is published, so restart cannot accept an older grant.
func commitSecureWatermark(path string, p *secureRegistry, digest string, initialize bool) error {
	markPath := path + ".accepted"
	data, err := readSecureFile(markPath, 4096)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) && !initialize {
		return errors.New("durable registry watermark missing; explicit initialization or verified recovery required")
	}
	if err == nil {
		var old securePolicyWatermark
		if err := decodeSecureJSON(data, &old); err != nil {
			return errors.New("invalid durable registry watermark")
		}
		hash, hashErr := hex.DecodeString(old.SHA256)
		if old.SchemaVersion != securePolicyVersion || old.Generation == 0 || hashErr != nil || len(hash) != sha256.Size || old.SHA256 != strings.ToLower(old.SHA256) {
			return errors.New("invalid durable registry watermark")
		}
		if p.Generation < old.Generation || (p.Generation == old.Generation && digest != old.SHA256) {
			return errors.New("registry generation rollback or same-generation mutation")
		}
		if p.Generation == old.Generation {
			return nil
		}
	}
	data, err = json.Marshal(securePolicyWatermark{SchemaVersion: securePolicyVersion, Generation: p.Generation, SHA256: digest})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(markPath), ".reverse-registry-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := replaceSecureFile(tmp, markPath); err != nil {
		return err
	}
	return syncSecureDirectory(filepath.Dir(markPath))
}

func loadSecureRoots(path string) (*x509.CertPool, error) {
	data, err := readSecureFile(path, secureMaxPolicyBytes)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(data)) > 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("trust file contains non-certificate content")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("trust file requires only CA certificate PEM blocks")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, errors.New("trust file contains a non-CA or invalid certificate")
		}
		pool.AddCert(cert)
		count++
		if count > 64 {
			return nil, errors.New("trust file exceeds 64 CA certificates")
		}
		data = rest
	}
	if count == 0 {
		return nil, errors.New("trust file has no valid CA certificates")
	}
	return pool, nil
}

func runSecureRegistry(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: zhreverse registry init --egress-registry-file <path> --hub-id <id>")
	}
	fs := flag.NewFlagSet("registry init", flag.ContinueOnError)
	path := fs.String("egress-registry-file", "", "new generation-1 registry JSON path")
	hubID := fs.String("hub-id", "", "expected Hub identity")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *path == "" || !validSecureID(*hubID) {
		return errors.New("registry init requires registry path and valid Hub identity")
	}
	return initializeSecureRegistry(*path, *hubID)
}

func initializeSecureRegistry(path, hubID string) error {
	canonical, err := canonicalSecureRegistryPath(path)
	if err != nil {
		return err
	}
	lock, err := lockSecureRegistry(canonical)
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(canonical + ".accepted"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("registry initialization refuses an existing or unreadable watermark")
	}
	p, digest, err := loadSecureRegistry(canonical, hubID)
	if err != nil {
		return err
	}
	if p.Generation != 1 {
		return errors.New("new registry initialization requires generation 1")
	}
	return commitSecureWatermark(canonical, p, digest, true)
}

func canonicalSecureRegistryPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := validateSecureDirectoryPath(abs); err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func secureCertificateIdentity(cert *x509.Certificate, role, expectedID string) (string, error) {
	if cert.IsCA || len(cert.URIs) != 1 || len(cert.ExtKeyUsage) != 1 || len(cert.UnknownExtKeyUsage) != 0 || cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return "", errors.New("leaf must have a single role URI and dedicated extended key usage")
	}
	wantUsage := x509.ExtKeyUsageClientAuth
	if role == "hub" {
		wantUsage = x509.ExtKeyUsageServerAuth
	}
	if cert.ExtKeyUsage[0] != wantUsage {
		return "", errors.New("certificate role key usage mismatch")
	}
	uri := cert.URIs[0]
	prefix := "/" + role + "/"
	if uri.Scheme != "spiffe" || uri.Host != "zhvpn" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || uri.RawPath != "" || !strings.HasPrefix(uri.Path, prefix) {
		return "", errors.New("certificate role URI mismatch")
	}
	id := strings.TrimPrefix(uri.Path, prefix)
	if !validSecureID(id) || (expectedID != "" && id != expectedID) {
		return "", errors.New("certificate identity mismatch")
	}
	return id, nil
}

func verifySecureChain(certs []*x509.Certificate, roots *x509.CertPool, usage x509.ExtKeyUsage, name string, now time.Time) error {
	if len(certs) == 0 {
		return errors.New("certificate missing")
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}, DNSName: name})
	return err
}

func parseSecureLocalCertificate(certFile, keyFile, role, identity string, roots *x509.CertPool) (tls.Certificate, []*x509.Certificate, error) {
	certPEM, err := readSecureFileWithSecret(certFile, secureMaxPolicyBytes, certFile == keyFile)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyPEM := certPEM
	if keyFile != certFile {
		keyPEM, err = readSecureFileWithSecret(keyFile, secureMaxPolicyBytes, true)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	var chain []*x509.Certificate
	for _, der := range pair.Certificate {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
		chain = append(chain, cert)
	}
	if _, err := secureCertificateIdentity(chain[0], role, identity); err != nil {
		return tls.Certificate{}, nil, err
	}
	usage := x509.ExtKeyUsageClientAuth
	if role == "hub" {
		usage = x509.ExtKeyUsageServerAuth
	}
	if err := verifySecureChain(chain, roots, usage, "", time.Now()); err != nil {
		return tls.Certificate{}, nil, err
	}
	pair.Leaf = chain[0]
	return pair, chain, nil
}

func authorizeSecureEgress(p *secureRegistry, certs []*x509.Certificate, roots *x509.CertPool, now time.Time) (secureSessionIdentity, error) {
	var result secureSessionIdentity
	if err := verifySecureChain(certs, roots, x509.ExtKeyUsageClientAuth, "", now); err != nil {
		return result, fmt.Errorf("certificate trust/expiry: %w", err)
	}
	id, err := secureCertificateIdentity(certs[0], "egress", "")
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(certs[0].Raw)
	fingerprint := hex.EncodeToString(sum[:])
	for _, e := range p.Egresses {
		if e.EgressID != id {
			continue
		}
		if e.State != "active" {
			return result, errors.New("egress revoked")
		}
		for _, c := range e.Credentials {
			if c.LeafSHA256 != fingerprint {
				continue
			}
			if c.State != "active" {
				return result, errors.New("credential revoked")
			}
			if now.Before(c.NotBefore) || !now.Before(c.ExpiresAt) {
				return result, errors.New("credential outside validity")
			}
			if c.NotBefore.Before(certs[0].NotBefore) || c.ExpiresAt.After(certs[0].NotAfter) {
				return result, errors.New("credential validity exceeds certificate")
			}
			return secureSessionIdentity{ProtocolVersion: 2, Transport: secureTCPTransport, EgressID: id, CredentialID: c.CredentialID, LeafSHA256: fingerprint, ExpiresAt: c.ExpiresAt}, nil
		}
		return result, errors.New("credential not registered")
	}
	return result, errors.New("egress not registered")
}
