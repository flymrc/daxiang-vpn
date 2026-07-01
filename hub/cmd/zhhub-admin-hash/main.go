package main

import (
	"fmt"
	"log"
	"os"

	"zongheng-vpn/hub/admin"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: go run ./hub/cmd/zhhub-admin-hash <password>")
	}
	hash, err := admin.HashPassword(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(hash)
}
