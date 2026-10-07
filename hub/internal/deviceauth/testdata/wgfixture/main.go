// Fixture executable only: uses files beside its own binary and never WG.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	if len(os.Args) > 1 && os.Args[1] == "child" {
		_ = os.WriteFile(filepath.Join(dir, "child.pid"), []byte(fmt.Sprint(os.Getpid())), 0600)
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(filepath.Join(dir, "escaped"), []byte("unexpected detached side effect"), 0600)
		time.Sleep(time.Hour)
		return
	}
	mode, _ := os.ReadFile(filepath.Join(dir, "mode"))
	if string(mode) == "hang" || string(mode) == "hang-detached" {
		child := exec.Command(exe, "child")
		if string(mode) == "hang-detached" {
			detach(child)
		}
		_ = child.Start()
		_ = os.WriteFile(filepath.Join(dir, "parent.pid"), []byte(fmt.Sprint(os.Getpid())), 0600)
		_ = os.WriteFile(filepath.Join(dir, "supervisor.pid"), []byte(fmt.Sprint(os.Getppid())), 0600)
		fmt.Fprintln(os.Stderr, "synthetic-secret-stderr")
		time.Sleep(time.Hour)
		return
	}
	data, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	peers := map[string][]string{}
	_ = json.Unmarshal(data, &peers)
	args := os.Args[1:]
	encoded, _ := json.Marshal(args)
	f, _ := os.OpenFile(filepath.Join(dir, "args.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	_, _ = f.Write(append(encoded, '\n'))
	f.Close()
	if len(args) == 3 && args[0] == "show" && args[2] == "allowed-ips" {
		for key, ips := range peers {
			v := strings.Join(ips, ",")
			if v == "" {
				v = "(none)"
			}
			fmt.Printf("%s\t%s\n", key, v)
		}
		return
	}
	if len(args) == 6 && args[0] == "set" && args[2] == "peer" && args[4] == "allowed-ips" {
		peers[args[3]] = []string{args[5]}
	} else if len(args) == 5 && args[0] == "set" && args[2] == "peer" && args[4] == "remove" {
		delete(peers, args[3])
	} else {
		os.Exit(42)
	}
	encoded, _ = json.Marshal(peers)
	_ = os.WriteFile(filepath.Join(dir, "state.json"), encoded, 0600)
}
