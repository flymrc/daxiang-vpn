// This helper lives only in an owned Linux network namespace. It emits finite
// receipts, never WireGuard material or raw command/HTTP response diagnostics.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"zongheng-vpn/shared/proxygate"
)

type request struct {
	Action, KeyFile, Endpoint, Address, LinkAddress, Proxy, Target, Echo string
	UntilUnixNano                                                        int64
}
type receipt struct {
	PID      int  `json:"pid,omitempty"`
	OK       bool `json:"ok"`
	Status   int  `json:"status,omitempty"`
	Marker   bool `json:"marker,omitempty"`
	Closed   bool `json:"closed,omitempty"`
	TimedOut bool `json:"timed_out,omitempty"`
}

func command(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	return c.Run()
}
func setup(r request) error {
	steps := [][]string{{"ip", "link", "set", "lo", "up"}, {"ip", "address", "add", r.LinkAddress, "dev", "vethpeer"}, {"ip", "link", "set", "vethpeer", "up"}, {"ip", "link", "add", "wgpeer", "type", "wireguard"}, {"wg", "set", "wgpeer", "private-key", r.KeyFile, "peer", os.Getenv("ZH_BARRIER_HUB_PUBLIC"), "endpoint", r.Endpoint, "allowed-ips", "10.250.0.1/32", "persistent-keepalive", "1"}, {"ip", "address", "add", r.Address, "dev", "wgpeer"}, {"ip", "link", "set", "wgpeer", "up"}, {"ip", "route", "add", "10.250.0.1/32", "dev", "wgpeer"}}
	for _, s := range steps {
		if command(s[0], s[1:]...) != nil {
			return errors.New("setup_failed")
		}
	}
	return nil
}
func connect(r request, striped bool) (net.Conn, *bufio.Reader, int, error) {
	c, e := net.DialTimeout("tcp", r.Proxy, time.Second)
	if e != nil {
		return nil, nil, 0, e
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	header := ""
	if striped {
		header = "X-ZH-Striped-Streams: 2\r\n"
	}
	if _, e = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", r.Target, r.Target, header); e != nil {
		c.Close()
		return nil, nil, 0, e
	}
	b := bufio.NewReader(c)
	res, e := http.ReadResponse(b, &http.Request{Method: http.MethodConnect})
	if e != nil {
		c.Close()
		return nil, nil, 0, e
	}
	if res.StatusCode != 200 {
		c.Close()
		return nil, nil, res.StatusCode, nil
	}
	return c, b, res.StatusCode, nil
}
func main() {
	if len(os.Args) == 4 && os.Args[1] == "--controller" {
		controllerMain(os.Args[2], os.Args[3])
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.Encode(receipt{PID: os.Getpid(), OK: true})
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 4096), 32768)
	var held net.Conn
	var heldReader *bufio.Reader
	var setupRequest request
	defer func() {
		if held != nil {
			held.Close()
		}
	}()
	for s.Scan() {
		var r request
		if json.Unmarshal(s.Bytes(), &r) != nil {
			enc.Encode(receipt{})
			continue
		}
		out := receipt{}
		switch r.Action {
		case "setup":
			out.OK = setup(r) == nil
			setupRequest = r
		case "refresh":
			pub := os.Getenv("ZH_BARRIER_HUB_PUBLIC")
			out.OK = command("wg", "set", "wgpeer", "peer", pub, "remove") == nil && command("wg", "set", "wgpeer", "peer", pub, "endpoint", setupRequest.Endpoint, "allowed-ips", "10.250.0.1/32", "persistent-keepalive", "1") == nil
		case "fetch":
			client := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			res, e := client.Get("http://" + r.Proxy + "/fetch?url=" + r.Target)
			if e == nil {
				out.Status = res.StatusCode
				body, _ := io.ReadAll(io.LimitReader(res.Body, 65536))
				res.Body.Close()
				out.Marker = strings.Contains(string(body), "owned-barrier-marker")
				out.OK = true
			}
		case "connect", "striped":
			c, b, status, e := connect(r, r.Action == "striped")
			out.Status = status
			out.OK = e == nil
			if c != nil {
				fmt.Fprint(c, "GET /marker HTTP/1.1\r\nHost: owned\r\nConnection: close\r\n\r\n")
				body, _ := io.ReadAll(io.LimitReader(b, 65536))
				out.Marker = strings.Contains(string(body), "owned-barrier-marker")
				c.Close()
			}
		case "hold":
			if held != nil {
				held.Close()
			}
			r.Target = r.Echo
			c, b, status, e := connect(r, false)
			out.Status = status
			if e == nil && c != nil {
				line, e := b.ReadString('\n')
				if e == nil && line == "owned-ready\n" {
					held = c
					heldReader = b
					held.SetDeadline(time.Time{})
					out.OK = true
				} else {
					c.Close()
				}
			}
		case "held":
			if held == nil {
				out.Closed = true
				out.OK = true
				break
			}
			held.SetDeadline(time.Now().Add(700 * time.Millisecond))
			_, e := fmt.Fprint(held, "owned-probe\n")
			if e == nil {
				var line string
				line, e = heldReader.ReadString('\n')
				if e == nil && line != "owned-probe\n" {
					out.OK = false
					break
				}
			}
			if ne, ok := e.(net.Error); ok && ne.Timeout() {
				out.OK = false
				break
			}
			var ne net.Error
			out.TimedOut = errors.As(e, &ne) && ne.Timeout()
			out.Closed = e != nil && !out.TimedOut
			out.OK = true
		case "stop":
			enc.Encode(receipt{OK: true})
			return
		}
		enc.Encode(out)
	}
}

// This mode uses the real protected policy reader and receiver-issued UDS
// controller session. SIGKILL bypasses every defer, so EOF is actual OS evidence.
func controllerMain(policyPath, socket string) {
	enc := json.NewEncoder(os.Stdout)
	policy, err := proxygate.LoadPolicy(policyPath)
	if err != nil {
		enc.Encode(receipt{PID: os.Getpid()})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	control, err := proxygate.DialControl(ctx, socket, *policy)
	cancel()
	if err != nil {
		enc.Encode(receipt{PID: os.Getpid()})
		return
	}
	defer control.Close()
	enc.Encode(receipt{PID: os.Getpid(), OK: true})
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 4096), 32768)
	for s.Scan() {
		var r request
		if json.Unmarshal(s.Bytes(), &r) != nil {
			enc.Encode(receipt{})
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		switch r.Action {
		case "grant":
			err = control.GrantUntil(ctx, time.Unix(0, r.UntilUnixNano))
		case "closed":
			err = control.Closed(ctx)
		case "stop":
			cancel()
			enc.Encode(receipt{OK: true})
			return
		default:
			err = errors.New("invalid_action")
		}
		cancel()
		enc.Encode(receipt{OK: err == nil})
	}
}
