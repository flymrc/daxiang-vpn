package main

import (
	"fmt"
	"os"

	"daxiang-vpn/frontend/dxvpn/internal/egressapp"
)

func main() {
	if err := egressapp.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}
