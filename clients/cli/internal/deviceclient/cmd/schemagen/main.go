package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"zongheng-vpn/clients/cli/internal/deviceclient"
)

func main() {
	check := flag.Bool("check", false, "check startup bytes and existing device schema fields/enums")
	flag.Parse()
	if flag.NArg() != 0 {
		os.Exit(2)
	}
	root := filepath.Join("clients", "cli", "internal", "deviceclient")
	b, e := deviceclient.StartReceiptSchema()
	if e != nil {
		fail("device start schema generation failed")
	}
	target := filepath.Join(root, "start-receipt.schema.json")
	if *check {
		actual, e := os.ReadFile(target)
		if e != nil || !bytes.Equal(actual, b) {
			fail("device start schema drift")
		}
	} else if os.WriteFile(target, b, 0644) != nil {
		fail("device start schema write failed")
	}
	old, e := os.ReadFile(filepath.Join(root, "receipt.schema.json"))
	if e != nil || deviceclient.CheckReceiptSchema(old) != nil {
		fail("device mutation schema drift")
	}
}
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
