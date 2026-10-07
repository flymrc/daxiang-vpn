package updateclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"zongheng-vpn/shared/updateverify"
)

var publicKeyID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

var receiptCodes = []string{"invalid_arguments", "policy_approval_mismatch", "invalid_policy", "policy_revision_refused", "scope_mismatch", "registration_required", "registration_incomplete", "already_registered", "corrupt_state", "local_storage_failure", "invalid_input_file", "metadata_refused", "signature_refused", "previous_refused", "replay_refused", "rollback_refused", "artifact_refused", "command_cancelled", "commit_unknown"}

func validCode(code string) bool {
	for _, allowed := range receiptCodes {
		if code == allowed {
			return true
		}
	}
	return false
}

func rejected(command string, err error) Receipt {
	code := "local_storage_failure"
	unknown := false
	var f *failure
	if errors.As(err, &f) {
		code = f.code
		unknown = f.unknown
	} else {
		switch {
		case errors.Is(err, updateverify.ErrPolicy):
			code = "invalid_policy"
		case errors.Is(err, updateverify.ErrMetadata):
			code = "metadata_refused"
		case errors.Is(err, updateverify.ErrSignature):
			code = "signature_refused"
		case errors.Is(err, updateverify.ErrPrevious):
			code = "previous_refused"
		case errors.Is(err, updateverify.ErrReplay):
			code = "replay_refused"
		case errors.Is(err, updateverify.ErrRollback):
			code = "rollback_refused"
		case errors.Is(err, updateverify.ErrArtifact):
			code = "artifact_refused"
		}
	}
	if !validCode(code) {
		code = "local_storage_failure"
		unknown = false
	}
	outcome := "rejected"
	if unknown {
		outcome = "result_unknown"
		code = "commit_unknown"
	}
	return Receipt{ContractVersion: Version, Command: command, Outcome: outcome, Code: code, CommitUnknown: unknown}
}

// DecodeReceipt is a strict public consumer; a valid receipt does not prove it
// came from a trusted CLI. Only verify may return a newly committed staging result.
func DecodeReceipt(raw []byte) (Receipt, error) {
	var r Receipt
	if strictDecode(raw, &r, MaxStateBytes) != nil || r.ContractVersion != Version || (!validCommand(r.Command) && r.Command != "unknown") {
		return r, fail("corrupt_state")
	}
	if !r.OK {
		if !validCode(r.Code) || r.Scope != nil || r.RegistrationID != "" || r.PolicyRevision != 0 || r.PolicySHA256 != "" || r.Floors != nil || r.Staging != nil || r.WatermarkCommitted || (r.CommitUnknown && (r.Code != "commit_unknown" || r.Outcome != "result_unknown")) || (!r.CommitUnknown && (r.Code == "commit_unknown" || r.Outcome != "rejected")) {
			return r, fail("corrupt_state")
		}
		return r, nil
	}
	if r.Command == "unknown" || r.Code != "" || r.CommitUnknown || r.Scope == nil || !validScope(*r.Scope) || !exactHex(r.RegistrationID, 16) || !safeRevision(r.PolicyRevision) || !exactHex(r.PolicySHA256, 32) || r.Floors == nil || !safeRevision(r.Floors.MinSequence) || !safeRevision(r.Floors.SecurityFloor) || !safeRevision(r.Floors.SecurityVersion) || r.Floors.SecurityVersion < r.Floors.SecurityFloor || r.Floors.MinProtocol < 1 || r.Floors.MinProtocol > 1024 || !atLeastVersion(r.Floors.MinVersion, r.Floors.MinVersion) {
		return r, fail("corrupt_state")
	}
	compatible := (r.Command == "enroll" && r.Outcome == "enrolled") || (r.Command == "policy-approve" && r.Outcome == "policy_approved") || (r.Command == "inspect" && r.Outcome == "inspected") || (r.Command == "verify" && r.Outcome == "watermark_committed")
	if !compatible || r.WatermarkCommitted != (r.Command == "verify") || (r.Staging != nil) != (r.Command == "verify") {
		return r, fail("corrupt_state")
	}
	if r.Staging != nil {
		s := r.Staging
		canonical, e := updateverify.CanonicalMetadata(s.Metadata)
		if e != nil || s.SchemaVersion != updateverify.SchemaVersion || s.Status != updateverify.StagingStatus || !publicKeyID.MatchString(s.KeyID) || hash(canonical) != s.MetadataSHA256 || s.VerifiedAt < s.Metadata.IssuedAt || s.VerifiedAt >= s.Metadata.ExpiresAt || (Scope{s.Metadata.Product, s.Metadata.Platform, s.Metadata.Architecture, s.Metadata.Channel}) != *r.Scope || !equalFloors(*r.Floors, mergeFloors(*r.Floors, receiptFloors(*s))) {
			return r, fail("corrupt_state")
		}
	}
	return r, nil
}

func encodeReceipt(out io.Writer, r Receipt) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return fmt.Errorf("%w: receipt encoding failed", ErrReported)
	}
	if _, e = DecodeReceipt(raw); e != nil {
		return fmt.Errorf("%w: receipt contract failed", ErrReported)
	}
	raw = append(raw, '\n')
	if out == nil {
		return fmt.Errorf("%w: output unavailable", ErrReported)
	}
	n, e := out.Write(raw)
	if e != nil || n != len(raw) {
		return fmt.Errorf("%w: output failed", ErrReported)
	}
	if !r.OK {
		return ErrReported
	}
	return nil
}
