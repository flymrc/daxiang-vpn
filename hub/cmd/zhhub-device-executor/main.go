// Dedicated trusted Linux WG child supervisor. Never prints private parameters.
package main

import (
	"os"
	"zongheng-vpn/hub/internal/deviceauth"
)

func main() { os.Exit(deviceauth.RunExecutionSupervisor(os.Args[1:])) }
