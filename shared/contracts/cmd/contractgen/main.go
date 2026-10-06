package main

import (
	"flag"
	"fmt"
	"os"

	"zongheng-vpn/shared/contracts"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "reject missing or changed generated artifacts")
	flag.Parse()
	if err := contracts.Generate(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
