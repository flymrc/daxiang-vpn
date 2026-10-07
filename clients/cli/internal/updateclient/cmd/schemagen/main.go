package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"zongheng-vpn/clients/cli/internal/updateclient"
)

func main() {
	check := flag.Bool("check", false, "refuse generated drift")
	flag.Parse()
	if flag.NArg() != 0 {
		os.Exit(2)
	}
	root := filepath.Join("clients", "cli", "internal", "updateclient")
	items := []struct {
		name     string
		generate func() ([]byte, error)
	}{{"receipt-v1.schema.json", updateclient.ReceiptSchema}, {"policy-v1.schema.json", updateclient.PolicySchema}}
	for _, item := range items {
		data, e := item.generate()
		if e != nil {
			fmt.Fprintln(os.Stderr, "update schema generation failed")
			os.Exit(1)
		}
		target := filepath.Join(root, item.name)
		if *check {
			existing, e := os.ReadFile(target)
			if e != nil || !bytes.Equal(existing, data) {
				fmt.Fprintln(os.Stderr, "update schema drift:", item.name)
				os.Exit(1)
			}
		} else if e = os.WriteFile(target, data, 0644); e != nil {
			fmt.Fprintln(os.Stderr, "update schema write failed")
			os.Exit(1)
		}
	}
}
