package proxygate

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestControllerRejectsExpiredGrantAcknowledgement(t *testing.T) {
	p := testPolicy()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := bufio.NewReaderSize(server, MaxMessageBytes+1)
		hello := message{Version: Version, Binding: p.SHA256(), Session: strings.Repeat("a", 32), Nonce: strings.Repeat("b", 32), Action: "hello"}
		if writeMessage(server, hello, time.Now().Add(time.Second)) != nil {
			return
		}
		closed, e := readMessage(server, r, time.Now().Add(time.Second))
		if e != nil {
			return
		}
		closed.Action = "ack"
		if writeMessage(server, closed, time.Now().Add(time.Second)) != nil {
			return
		}
		grant, e := readMessage(server, r, time.Now().Add(time.Second))
		if e != nil {
			return
		}
		time.Sleep(time.Until(time.Unix(0, grant.UntilUnixNano)) + 20*time.Millisecond)
		grant.Action = "ack"
		writeMessage(server, grant, time.Now().Add(time.Second))
	}()
	c, e := connectController(context.Background(), client, p)
	if e != nil {
		t.Fatal(e)
	}
	if e := c.GrantUntil(context.Background(), time.Now().Add(150*time.Millisecond)); e == nil {
		t.Fatal("expired grant ACK was accepted")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("peer leaked")
	}
}
