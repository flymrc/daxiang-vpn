package updateclient

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
	"zongheng-vpn/shared/updateverify"
)

type storage interface {
	read(string) ([]byte, error)
	create(string, []byte) error
	write(string, []byte) error
}

type privateStore struct{ home paths.Context }

func (s privateStore) read(name string) ([]byte, error) {
	p, e := proxy.NewPrivateState(s.home, name)
	if e != nil {
		return nil, e
	}
	return p.Read()
}
func (s privateStore) create(name string, raw []byte) error {
	p, e := proxy.NewPrivateState(s.home, name)
	if e != nil {
		return e
	}
	return p.Create(raw)
}
func (s privateStore) write(name string, raw []byte) error {
	p, e := proxy.NewPrivateState(s.home, name)
	if e != nil {
		return e
	}
	return p.Write(raw)
}

func newID() (string, error) {
	data := make([]byte, 16)
	if _, e := rand.Read(data); e != nil {
		return "", e
	}
	return hex.EncodeToString(data), nil
}

func load(s storage, scope Scope, now time.Time) (registration, state, updateverify.Policy, error) {
	var a registration
	var st state
	aBytes, aErr := s.read(RegistrationName)
	sBytes, sErr := s.read(StateName)
	aMissing, sMissing := errors.Is(aErr, os.ErrNotExist), errors.Is(sErr, os.ErrNotExist)
	if aMissing && sMissing {
		return a, st, updateverify.Policy{}, fail("registration_required")
	}
	if aMissing || sMissing {
		return a, st, updateverify.Policy{}, fail("registration_incomplete")
	}
	if aErr != nil || sErr != nil {
		return a, st, updateverify.Policy{}, fail("local_storage_failure")
	}
	if strictDecode(aBytes, &a, MaxStateBytes) != nil || strictDecode(sBytes, &st, MaxStateBytes) != nil || a.SchemaVersion != Version || st.SchemaVersion != Version || !exactHex(a.ID, 16) || a.ID != st.ID || a.Scope != st.Scope || !validScope(a.Scope) || !safeRevision(a.InitialRevision) || !safeRevision(st.PolicyRevision) || st.PolicyRevision < a.InitialRevision || !exactHex(a.InitialSHA256, 32) || hash([]byte(a.InitialPolicyJSON)) != a.InitialSHA256 || !exactHex(st.PolicySHA256, 32) || hash([]byte(st.PolicyJSON)) != st.PolicySHA256 || st.HasPrevious != (st.Previous != nil) {
		return a, st, updateverify.Policy{}, fail("corrupt_state")
	}
	if st.Scope != scope {
		return a, st, updateverify.Policy{}, fail("scope_mismatch")
	}
	initial, initialPolicy, e := decodePolicy([]byte(a.InitialPolicyJSON))
	if e != nil || initial.Scope != a.Scope || initial.Revision != a.InitialRevision {
		return a, st, updateverify.Policy{}, fail("corrupt_state")
	}
	doc, p, e := decodePolicy([]byte(st.PolicyJSON))
	if e != nil || doc.Scope != st.Scope || doc.Revision != st.PolicyRevision || !policyNotBelow(p, policyFloors(initialPolicy)) || (st.PolicyRevision == a.InitialRevision && st.PolicySHA256 != a.InitialSHA256) {
		return a, st, p, fail("corrupt_state")
	}
	expected := mergeFloors(policyFloors(initialPolicy), policyFloors(p))
	if st.Previous != nil {
		if updateverify.ValidatePrevious(*st.Previous, p, now) != nil {
			return a, st, p, fail("corrupt_state")
		}
		expected = mergeFloors(expected, receiptFloors(*st.Previous))
	}
	if !equalFloors(st.Floors, expected) {
		return a, st, p, fail("corrupt_state")
	}
	return a, st, effectivePolicy(p, st.Floors), nil
}

// Any attempted commit whose I/O reports an error is uncertain. Even a matching
// readback is not used as a staging permit. Never restore old bytes as repair.
func commit(s storage, name string, value any, create bool) error {
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > MaxStateBytes {
		return fail("local_storage_failure")
	}
	if create {
		e = s.create(name, raw)
	} else {
		e = s.write(name, raw)
	}
	if e != nil {
		return &failure{code: "commit_unknown", unknown: true}
	}
	readback, e := s.read(name)
	if e != nil || string(readback) != string(raw) {
		return &failure{code: "commit_unknown", unknown: true}
	}
	return nil
}

func summary(command, outcome string, st state) Receipt {
	floors := st.Floors
	scope := st.Scope
	return Receipt{ContractVersion: Version, Command: command, OK: true, Outcome: outcome, Scope: &scope, RegistrationID: st.ID, PolicyRevision: st.PolicyRevision, PolicySHA256: st.PolicySHA256, Floors: &floors}
}
