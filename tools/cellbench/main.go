// cellbench: minimal cellular path benchmark for the Android egress phone.
// Runs as a static linux/arm64 binary from a Magisk root shell, like dxreverse.
//
//	cellbench rtt  <host:port> <rounds>
//	cellbench down <url> [stallTimeoutSec]
//	cellbench up   <url> <sizeMB> <rounds>
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func resolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
			if err == nil {
				return conn, nil
			}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
}

// AF=4 or AF=6 in the environment pins the address family so the
// Rakuten IPv4/CGNAT path can be compared against native IPv6.
func dialNetwork() string {
	switch os.Getenv("AF") {
	case "4":
		return "tcp4"
	case "6":
		return "tcp6"
	}
	return "tcp"
}

func httpClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, Resolver: resolver()}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				// PIN_IP skips DNS entirely while keeping the URL hostname for SNI.
				if pin := os.Getenv("PIN_IP"); pin != "" {
					_, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					addr = net.JoinHostPort(pin, port)
				}
				conn, err := dialer.DialContext(ctx, dialNetwork(), addr)
				if err == nil {
					fmt.Printf("dial: %s -> %s\n", addr, conn.RemoteAddr())
				}
				return conn, err
			},
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: cellbench rtt|down|up ...")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "rtt":
		rounds, _ := strconv.Atoi(os.Args[3])
		for i := 0; i < rounds; i++ {
			start := time.Now()
			conn, err := net.DialTimeout("tcp", os.Args[2], 10*time.Second)
			elapsed := time.Since(start)
			if err != nil {
				fmt.Printf("rtt %d: ERR %v\n", i, err)
				continue
			}
			conn.Close()
			fmt.Printf("rtt %d: %d ms\n", i, elapsed.Milliseconds())
			time.Sleep(500 * time.Millisecond)
		}
	case "down":
		stall := 15
		if len(os.Args) > 3 {
			stall, _ = strconv.Atoi(os.Args[3])
		}
		client := httpClient(0)
		start := time.Now()
		resp, err := client.Get(os.Args[2])
		if err != nil {
			fmt.Printf("down: GET err: %v\n", err)
			return
		}
		defer resp.Body.Close()
		fmt.Printf("down: status=%d ttfb=%dms\n", resp.StatusCode, time.Since(start).Milliseconds())
		buf := make([]byte, 64*1024)
		var total int64
		lastReport := time.Now()
		lastBytes := int64(0)
		for {
			_ = resp.Body.(interface{}) // keep simple; stall handled via read deadline below
			done := make(chan struct{})
			var n int
			var rerr error
			go func() { n, rerr = resp.Body.Read(buf); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Duration(stall) * time.Second):
				fmt.Printf("down: STALL >%ds at %d bytes after %dms\n", stall, total, time.Since(start).Milliseconds())
				return
			}
			total += int64(n)
			if time.Since(lastReport) >= 2*time.Second {
				rate := float64(total-lastBytes) * 8 / time.Since(lastReport).Seconds() / 1e6
				fmt.Printf("down: +%dms total=%d window=%.2f Mbps\n", time.Since(start).Milliseconds(), total, rate)
				lastReport = time.Now()
				lastBytes = total
			}
			if rerr != nil {
				break
			}
		}
		secs := time.Since(start).Seconds()
		fmt.Printf("down: DONE %d bytes in %.1fs = %.2f Mbps\n", total, secs, float64(total)*8/secs/1e6)
	case "up":
		sizeMB, _ := strconv.Atoi(os.Args[3])
		rounds, _ := strconv.Atoi(os.Args[4])
		client := httpClient(90 * time.Second)
		payload := make([]byte, sizeMB*1024*1024)
		for i := 0; i < rounds; i++ {
			start := time.Now()
			req, _ := http.NewRequest(http.MethodPost, os.Args[2], &progressReader{data: payload, start: start})
			req.ContentLength = int64(len(payload))
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := client.Do(req)
			elapsed := time.Since(start).Seconds()
			if err != nil {
				fmt.Printf("up %d: ERR after %.1fs: %v\n", i, elapsed, err)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			fmt.Printf("up %d: status=%d %dMB in %.1fs = %.2f Mbps\n", i, resp.StatusCode, sizeMB, elapsed, float64(len(payload))*8/elapsed/1e6)
		}
	}
}

type progressReader struct {
	data  []byte
	off   int
	start time.Time
	last  time.Time
}

func (r *progressReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	if time.Since(r.last) >= 2*time.Second {
		fmt.Printf("up: +%dms sent=%d\n", time.Since(r.start).Milliseconds(), r.off)
		r.last = time.Now()
	}
	return n, nil
}
