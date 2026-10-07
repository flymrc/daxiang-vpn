// Offline authority comparison only. No database replacement or WG mutation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
)

func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("zhhub-device-restore-plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backup := flags.String("backup", "", "standalone offline backup database")
	latest := flags.String("latest-authority", "", "explicit verified standalone latest authority database")
	checkpoint := flags.String("latest-checkpoint", "", "caller-confirmed latest authority checkpoint JSON")
	asof := flags.String("as-of", "", "explicit RFC3339 planning time")
	age := flags.Duration("max-checkpoint-age", 0, "explicit budget between 1s and 24h")
	peers := flags.String("observed-peers", "", "optional offline JSON peer inventory; never reads live WG")
	refuse := func() int {
		fmt.Fprintln(errOut, "Offline restore plan refused. Check standalone snapshots, exact schema, checkpoint freshness and retained authority history.")
		return 1
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 || *backup == "" || *latest == "" || *checkpoint == "" {
		return refuse()
	}
	at, err := time.Parse(time.RFC3339Nano, *asof)
	if err != nil {
		return refuse()
	}
	q := deviceauth.RestorePlanRequest{BackupPath: *backup, LatestAuthorityPath: *latest, LatestCheckpointPath: *checkpoint, AsOf: at, MaxCheckpointAge: *age}
	if *peers != "" {
		info, e := os.Lstat(*peers)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return refuse()
		}
		f, e := os.Open(*peers)
		if e != nil {
			return refuse()
		}
		b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if e != nil || int64(len(b)) != info.Size() {
			return refuse()
		}
		var values []struct {
			PublicKey  string   `json:"public_key"`
			AllowedIPs []string `json:"allowed_ips"`
		}
		if deviceauth.RestoreStrictJSON(b, &values) != nil {
			return refuse()
		}
		for _, p := range values {
			q.ObservedPeers = append(q.ObservedPeers, deviceauth.Peer{PublicKey: p.PublicKey, AllowedIPs: p.AllowedIPs})
		}
	}
	plan, err := deviceauth.PlanOfflineRestore(ctx, q)
	if err != nil {
		return refuse()
	}
	if json.NewEncoder(out).Encode(plan) != nil {
		return refuse()
	}
	return 0
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
