package main

import (
	"fmt"
	"os"

	"daxiang-vpn/frontend/dxvpn/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}
