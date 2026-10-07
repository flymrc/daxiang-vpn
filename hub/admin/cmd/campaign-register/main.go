// Command campaign-register seals a provisional, human-approved inventory.
// It cannot start a campaign, set T0, or create a compliant receipt.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"zongheng-vpn/hub/admin/internal/db"
)

type receipt struct {
	Version       int        `json:"contract_version"`
	OK            bool       `json:"ok"`
	Code          string     `json:"code"`
	Outcome       string     `json:"outcome"`
	RegistryID    string     `json:"registry_id"`
	ApprovedSHA   string     `json:"approved_inventory_sha256"`
	BaselineCount int        `json:"baseline_member_count"`
	T0            *time.Time `json:"t0"`
	Ready         bool       `json:"ready"`
}

func run(args []string, out io.Writer) int {
	result := receipt{Version: 1, Code: "invalid_input", Outcome: "refused"}
	emit := func(code string) int { result.Code = code; _ = json.NewEncoder(out).Encode(result); return 1 }
	fs := flag.NewFlagSet("campaign-register", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbpath := fs.String("db", "", "")
	input := fs.String("inventory-file", "", "")
	approved := fs.String("approve-inventory-sha256", "", "")
	// Duplicate option spellings do not silently select the last approval/path.
	seen := map[string]bool{}
	for index := 0; index < len(args); index += 2 {
		if index+1 >= len(args) || (args[index] != "--db" && args[index] != "--inventory-file" && args[index] != "--approve-inventory-sha256") || seen[args[index]] {
			return emit("invalid_options")
		}
		seen[args[index]] = true
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 || len(seen) != 3 || !filepath.IsAbs(*dbpath) || !locationAllowed(*dbpath) || !locationAllowed(*input) {
		return emit("invalid_options")
	}
	raw, e := readInventoryInput(*input)
	if e != nil {
		return emit("invalid_input_file")
	}
	inventory, e := db.DecodeInventory(raw, *approved)
	if e != nil {
		if errors.Is(e, db.ErrInventoryApproval) {
			return emit("inventory_approval_required")
		}
		return emit("invalid_inventory")
	}
	// Approval and all finite parsing precede any DB/directory creation.
	if checkParents(*dbpath, true) != nil {
		return emit("invalid_database_path")
	}
	if info, e := os.Lstat(*dbpath); e == nil {
		if !info.Mode().IsRegular() {
			return emit("invalid_database_path")
		}
		f, e := openOrdinary(*dbpath)
		if e != nil {
			return emit("invalid_database_path")
		}
		_ = f.Close()
	} else if !os.IsNotExist(e) {
		return emit("invalid_database_path")
	}
	store, e := db.OpenStore(*dbpath)
	if e != nil {
		return emit("projection_unavailable")
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, e := store.RegisterProvisionalInventory(ctx, raw, *approved, time.Now())
	if e != nil {
		if errors.Is(e, db.ErrInventoryImmutable) {
			return emit("inventory_already_registered")
		}
		return emit("commit_unknown")
	}
	result.OK = true
	result.Code = "ok"
	result.Outcome = "registered_provisional"
	if !created {
		result.Outcome = "already_registered"
	}
	result.RegistryID = inventory.RegistryID
	result.ApprovedSHA = *approved
	result.BaselineCount = len(inventory.Members)
	if json.NewEncoder(out).Encode(result) != nil {
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
