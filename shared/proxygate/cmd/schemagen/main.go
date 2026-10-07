// Command schemagen projects/checks the canonical local gate contract. It has
// no runtime control authority.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"zongheng-vpn/shared/proxygate"
)

func main() {
	check := flag.Bool("check", false, "refuse generated schema drift")
	output := flag.String("output", "shared/proxygate/proxygate-v1.schema.json", "schema output path")
	flag.Parse()
	raw, e := proxygate.Schema()
	if e == nil && *check {
		var existing []byte
		existing, e = os.ReadFile(*output)
		if e == nil && !bytes.Equal(existing, raw) {
			e = fmt.Errorf("proxygate schema drift")
		}
	} else if e == nil {
		e = os.WriteFile(*output, raw, 0644)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
