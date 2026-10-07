package updateclient

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"
	"zongheng-vpn/shared/updateverify"
)

func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func safeRevision(revision uint64) bool {
	return revision > 0 && revision <= updateverify.MaxSafeInteger
}
func validScope(s Scope) bool {
	return updateverify.ValidScope(s.verifier())
}

func decodePolicy(raw []byte) (PolicyDocument, updateverify.Policy, error) {
	var doc PolicyDocument
	if strictDecode(raw, &doc, MaxPolicyBytes) != nil || doc.SchemaVersion != Version || !safeRevision(doc.Revision) || !validScope(doc.Scope) || doc.MaxValiditySeconds < 1 || doc.MaxValiditySeconds > int64(updateverify.MaxValidity/time.Second) {
		return doc, updateverify.Policy{}, fail("invalid_policy")
	}
	p := updateverify.Policy{Scope: doc.Scope.verifier(), MinVersion: doc.MinVersion, MinProtocol: doc.MinProtocol, MaxProtocol: doc.MaxProtocol, MinSequence: doc.MinSequence, SecurityFloor: doc.SecurityFloor, MaxArtifactSize: doc.MaxArtifactSize, MaxValidity: time.Duration(doc.MaxValiditySeconds) * time.Second}
	for _, key := range doc.Keys {
		pub, e := base64.StdEncoding.DecodeString(key.PublicKey)
		if e != nil || len(pub) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(pub) != key.PublicKey {
			return doc, p, fail("invalid_policy")
		}
		p.Keys = append(p.Keys, updateverify.Key{ID: key.ID, PublicKey: ed25519.PublicKey(pub), ValidFrom: key.ValidFrom, ValidUntil: key.ValidUntil, MinSequence: key.MinSequence, MaxSequence: key.MaxSequence})
	}
	if updateverify.ValidatePolicy(p) != nil {
		return doc, p, fail("invalid_policy")
	}
	return doc, p, nil
}

func policyFloors(p updateverify.Policy) Floors {
	return Floors{MinVersion: p.MinVersion, MinProtocol: p.MinProtocol, MinSequence: p.MinSequence, SecurityFloor: p.SecurityFloor, SecurityVersion: p.SecurityFloor}
}

func atLeastVersion(value, floor string) bool {
	cmp, e := updateverify.CompareVersions(value, floor)
	return e == nil && cmp >= 0
}
func maxVersion(a, b string) string {
	if atLeastVersion(a, b) {
		return a
	}
	return b
}

func mergeFloors(a, b Floors) Floors {
	return Floors{MinVersion: maxVersion(a.MinVersion, b.MinVersion), MinProtocol: max(a.MinProtocol, b.MinProtocol), MinSequence: max(a.MinSequence, b.MinSequence), SecurityFloor: max(a.SecurityFloor, b.SecurityFloor), SecurityVersion: max(a.SecurityVersion, b.SecurityVersion)}
}
func receiptFloors(r updateverify.Receipt) Floors {
	return Floors{MinVersion: r.Metadata.Version, MinSequence: r.Metadata.ReleaseSequence, SecurityFloor: r.Metadata.SecurityFloor, SecurityVersion: r.Metadata.SecurityVersion}
}

func equalFloors(a, b Floors) bool {
	cmp, err := updateverify.CompareVersions(a.MinVersion, b.MinVersion)
	return err == nil && cmp == 0 && a.MinProtocol == b.MinProtocol && a.MinSequence == b.MinSequence && a.SecurityFloor == b.SecurityFloor && a.SecurityVersion == b.SecurityVersion
}

func policyNotBelow(p updateverify.Policy, floor Floors) bool {
	return atLeastVersion(p.MinVersion, floor.MinVersion) && p.MinProtocol >= floor.MinProtocol && p.MinSequence >= floor.MinSequence && p.SecurityFloor >= floor.SecurityFloor
}

func effectivePolicy(p updateverify.Policy, floor Floors) updateverify.Policy {
	p.MinVersion = maxVersion(p.MinVersion, floor.MinVersion)
	p.MinProtocol = max(p.MinProtocol, floor.MinProtocol)
	p.MinSequence = max(p.MinSequence, floor.MinSequence)
	p.SecurityFloor = max(p.SecurityFloor, floor.SecurityFloor)
	return p
}
