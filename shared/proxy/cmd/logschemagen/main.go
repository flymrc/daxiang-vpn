package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"zongheng-vpn/shared/proxy"
)

func main() {
	check := flag.Bool("check", false, "check the current schema without writing")
	path := flag.String("output", "shared/proxy/engine-events-v1.schema.json", "schema path from the current directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	want := proxy.EngineLogSchema()
	if *check {
		actual, e := os.ReadFile(*path)
		if e != nil || !bytes.Equal(actual, want) {
			fmt.Fprintln(os.Stderr, "engine event schema drift or missing file")
			os.Exit(1)
		}
		return
	}
	if e := os.WriteFile(*path, want, 0644); e != nil {
		fmt.Fprintln(os.Stderr, "cannot write engine event schema")
		os.Exit(1)
	}
}
