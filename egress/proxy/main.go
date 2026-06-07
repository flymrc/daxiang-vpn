package main

import (
	"fmt"
	"os"

	"daxiang-vpn/egress/proxy/internal/egressapp"
)

func main() {
	if err := egressapp.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}
