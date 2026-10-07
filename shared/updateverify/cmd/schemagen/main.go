// Command schemagen projects/checks the public update envelope schema. This
// development command does not participate in runtime update authority.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"zongheng-vpn/shared/updateverify"
)

func main() {
	check := flag.Bool("check", false, "refuse generated schema drift")
	output := flag.String("output", "shared/updateverify/metadata-v1.schema.json", "schema output path")
	flag.Parse()
	raw, err := updateverify.Schema()
	if err == nil && *check {
		var existing []byte
		existing, err = os.ReadFile(*output)
		if err == nil && !bytes.Equal(existing, raw) {
			err = fmt.Errorf("update envelope schema drift")
		}
	} else if err == nil {
		err = os.WriteFile(*output, raw, 0644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
