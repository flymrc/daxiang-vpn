package updateclient

import (
	"context"
	"errors"
	"flag"
	"io"
	"strconv"
	"time"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
	"zongheng-vpn/shared/updateverify"
)

type options struct {
	command                                                        string
	scope                                                          Scope
	policyFile, approval, revisionText, metadataFile, artifactFile string
	revision                                                       uint64
	timeout                                                        time.Duration
}

func validCommand(command string) bool {
	return command == "enroll" || command == "verify" || command == "policy-approve" || command == "inspect"
}
func parse(args []string) (options, error) {
	o := options{command: "unknown"}
	if len(args) == 0 || !validCommand(args[0]) {
		return o, fail("invalid_arguments")
	}
	o.command = args[0]
	f := flag.NewFlagSet("update", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.scope.Product, "product", "", "explicit product")
	f.StringVar(&o.scope.Platform, "platform", "", "explicit platform")
	f.StringVar(&o.scope.Architecture, "architecture", "", "explicit architecture")
	f.StringVar(&o.scope.Channel, "channel", "", "explicit channel")
	f.StringVar(&o.policyFile, "policy-file", "", "operator-approved offline policy JSON")
	f.StringVar(&o.approval, "approve-policy-sha256", "", "human approval of exact policy bytes")
	f.StringVar(&o.revisionText, "policy-revision", "", "explicit canonical policy revision")
	f.StringVar(&o.metadataFile, "metadata-file", "", "signed candidate envelope")
	f.StringVar(&o.artifactFile, "artifact-file", "", "read-only candidate artifact")
	f.DurationVar(&o.timeout, "timeout", 15*time.Second, "command deadline")
	jsonOutput := f.Bool("json", true, "single public update receipt")
	// flag normally permits silent duplicate options. Reject them before parsing.
	seen := map[string]bool{}
	for _, arg := range args[1:] {
		if len(arg) > 0 && arg[0] == '-' {
			name := arg
			for len(name) > 0 && name[0] == '-' {
				name = name[1:]
			}
			for i, c := range name {
				if c == '=' {
					name = name[:i]
					break
				}
			}
			if seen[name] {
				return o, fail("invalid_arguments")
			}
			seen[name] = true
		}
	}
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || !*jsonOutput || !validScope(o.scope) || o.timeout < time.Millisecond || o.timeout > 30*time.Second {
		return o, fail("invalid_arguments")
	}
	visited := map[string]bool{}
	f.Visit(func(v *flag.Flag) { visited[v.Name] = true })
	for name := range visited {
		common := name == "product" || name == "platform" || name == "architecture" || name == "channel" || name == "timeout" || name == "json"
		approval := (o.command == "enroll" || o.command == "policy-approve") && (name == "policy-file" || name == "approve-policy-sha256" || name == "policy-revision")
		candidate := o.command == "verify" && (name == "metadata-file" || name == "artifact-file")
		if !common && !approval && !candidate {
			return o, fail("invalid_arguments")
		}
	}
	if o.command == "enroll" || o.command == "policy-approve" {
		rev, e := strconv.ParseUint(o.revisionText, 10, 64)
		if o.policyFile == "" || !exactHex(o.approval, 32) || e != nil || !safeRevision(rev) || strconv.FormatUint(rev, 10) != o.revisionText {
			return o, fail("invalid_arguments")
		}
		o.revision = rev
	}
	if o.command == "verify" && (o.metadataFile == "" || o.artifactFile == "") {
		return o, fail("invalid_arguments")
	}
	return o, nil
}

// Run never networks, installs, executes, repairs, resets, or loads Policy from
// candidate metadata. Every command, including inspect, shares the home lock.
func Run(ctx context.Context, home paths.Context, args []string, out, errOut io.Writer) error {
	_ = errOut
	o, e := parse(args)
	if e != nil {
		return encodeReceipt(out, rejected(o.command, e))
	}
	if ctx == nil {
		return encodeReceipt(out, rejected(o.command, fail("invalid_arguments")))
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if ctx.Err() != nil {
		return encodeReceipt(out, rejected(o.command, fail("command_cancelled")))
	}
	var r Receipt
	e = proxy.WithOperationLockContext(ctx, home, func() error { r = execute(ctx, privateStore{home}, o, time.Now); return nil })
	if e != nil {
		if ctx.Err() != nil {
			e = fail("command_cancelled")
		}
		r = rejected(o.command, e)
	}
	return encodeReceipt(out, r)
}

func ReportSetupFailure(out io.Writer) error {
	return encodeReceipt(out, rejected("unknown", fail("local_storage_failure")))
}

func execute(ctx context.Context, s storage, o options, clock func() time.Time) Receipt {
	if ctx.Err() != nil {
		return rejected(o.command, fail("command_cancelled"))
	}
	now := clock()
	_, st, p, e := load(s, o.scope, now)
	if o.command == "enroll" {
		if e == nil {
			return rejected(o.command, fail("already_registered"))
		}
		var f *failure
		if !errors.As(e, &f) || f.code != "registration_required" {
			return rejected(o.command, e)
		}
	} else if e != nil {
		return rejected(o.command, e)
	}
	if o.command == "inspect" {
		return summary(o.command, "inspected", st)
	}
	if o.command == "enroll" || o.command == "policy-approve" {
		raw, e := readInput(ctx, o.policyFile, MaxPolicyBytes)
		if e != nil {
			return rejected(o.command, e)
		}
		if hash(raw) != o.approval {
			return rejected(o.command, fail("policy_approval_mismatch"))
		}
		doc, approved, e := decodePolicy(raw)
		if e != nil {
			return rejected(o.command, e)
		}
		if doc.Scope != o.scope {
			return rejected(o.command, fail("scope_mismatch"))
		}
		if doc.Revision != o.revision {
			return rejected(o.command, fail("policy_revision_refused"))
		}
		if o.command == "policy-approve" && (doc.Revision <= st.PolicyRevision || !policyNotBelow(approved, st.Floors)) {
			return rejected(o.command, fail("policy_revision_refused"))
		}
		if ctx.Err() != nil {
			return rejected(o.command, fail("command_cancelled"))
		}
		if o.command == "enroll" {
			id, e := newID()
			if e != nil {
				return rejected(o.command, fail("local_storage_failure"))
			}
			a := registration{SchemaVersion: Version, ID: id, Scope: o.scope, InitialRevision: doc.Revision, InitialSHA256: o.approval, InitialPolicyJSON: string(raw)}
			st = state{SchemaVersion: Version, ID: id, Scope: o.scope, PolicyRevision: doc.Revision, PolicySHA256: o.approval, PolicyJSON: string(raw), Floors: policyFloors(approved)}
			if e = commit(s, RegistrationName, a, true); e != nil {
				return rejected(o.command, e)
			}
			if ctx.Err() != nil {
				return rejected(o.command, &failure{code: "commit_unknown", unknown: true})
			}
			if e = commit(s, StateName, st, true); e != nil {
				return rejected(o.command, e)
			}
		} else {
			st.PolicyRevision = doc.Revision
			st.PolicySHA256 = o.approval
			st.PolicyJSON = string(raw)
			st.Floors = mergeFloors(st.Floors, policyFloors(approved))
			if e = commit(s, StateName, st, false); e != nil {
				return rejected(o.command, e)
			}
		}
		if ctx.Err() != nil {
			return rejected(o.command, &failure{code: "commit_unknown", unknown: true})
		}
		outcome := "policy_approved"
		if o.command == "enroll" {
			outcome = "enrolled"
		}
		return summary(o.command, outcome, st)
	}
	metadata, e := readInput(ctx, o.metadataFile, updateverify.MaxEnvelopeBytes)
	if e != nil {
		return rejected(o.command, e)
	}
	artifact, before, e := openInput(o.artifactFile, p.MaxArtifactSize)
	if e != nil {
		return rejected(o.command, e)
	}
	defer artifact.Close()
	// Regular local files are read on the calling goroutine. The verifier checks
	// context between reads; there is no unbounded reader/background worker.
	r, e := updateverify.Verify(ctx, metadata, artifact, p, st.Previous, clock())
	if e != nil {
		if ctx.Err() != nil {
			return rejected(o.command, fail("command_cancelled"))
		}
		return rejected(o.command, e)
	}
	if !unchanged(artifact, before) {
		return rejected(o.command, fail("artifact_refused"))
	}
	// A slow read cannot commit an artifact whose expiry or signing-key window
	// has elapsed meanwhile. No test clock/expiry override is exposed in CLI.
	commitTime := clock().Unix()
	if commitTime < r.VerifiedAt || commitTime < r.Metadata.IssuedAt || commitTime >= r.Metadata.ExpiresAt {
		return rejected(o.command, updateverify.ErrMetadata)
	}
	keyActive := false
	for _, key := range p.Keys {
		if key.ID == r.KeyID && commitTime >= key.ValidFrom && commitTime < key.ValidUntil {
			keyActive = true
		}
	}
	if !keyActive {
		return rejected(o.command, updateverify.ErrSignature)
	}
	if r.Metadata.SecurityVersion < st.Floors.SecurityVersion {
		return rejected(o.command, updateverify.ErrRollback)
	}
	if ctx.Err() != nil {
		return rejected(o.command, fail("command_cancelled"))
	}
	st.Previous = &r
	st.HasPrevious = true
	st.Floors = mergeFloors(st.Floors, receiptFloors(r))
	if e = commit(s, StateName, st, false); e != nil {
		return rejected(o.command, e)
	}
	if ctx.Err() != nil {
		return rejected(o.command, &failure{code: "commit_unknown", unknown: true})
	}
	result := summary(o.command, "watermark_committed", st)
	result.WatermarkCommitted = true
	result.Staging = &r
	return result
}
