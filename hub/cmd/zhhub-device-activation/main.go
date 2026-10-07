// Trusted local administration only. Output contains a one-time activation
// credential and must be transferred through an authenticated secure channel.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
	"zongheng-vpn/hub/internal/deviceapi"
)

func main() {
	db := flag.String("db", "", "absolute private authority SQLite path")
	policy := flag.String("policy", "", "explicit policy JSON path")
	owner := flag.String("owner", "", "customer owner ID")
	until := flag.String("valid-until", "", "UTC RFC3339 authorization deadline")
	ttl := flag.Duration("activation-ttl", 10*time.Minute, "one-time activation lifetime, maximum 24h")
	flag.Parse()
	if flag.NArg() != 0 {
		fail()
	}
	deadline, err := time.Parse(time.RFC3339, *until)
	if err != nil {
		fail()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, handle, err := deviceapi.OpenLocalAuthority(ctx, *db, *policy)
	if err != nil {
		fail()
	}
	defer handle.Close()
	activation, err := s.IssueActivation(ctx, *owner, deadline, time.Now().UTC().Add(*ttl))
	if err != nil {
		fail()
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"activation_credential": activation.Credential, "valid_until": activation.ValidUntil, "activation_until": activation.ActivationUntil})
}
func fail() {
	fmt.Fprintln(os.Stderr, "activation issuance rejected; check private hosting, policy and explicit authorization parameters")
	os.Exit(1)
}
