// dxandroid-control: 出口手机上的极简 SSH 服务,用于脱离 ADB 的远程控制。
//
// 设计要点:
//   - 默认只绑定 WireGuard 隧道 IP 10.66.0.101:22,公网网卡上不可见;
//     只有进入隧道(10.66.0.0/24)的 peer 能连,配合 Hub 侧可再用 iptables 收紧。
//   - 仅公钥认证(读 authorized_keys),不支持密码。
//   - 用 IP_FREEBIND 允许在 tun0/地址尚未就绪时也能绑定,解决开机时序问题;
//     真正可达仍取决于隧道是否已建立。
//   - 进程由 Magisk 以 root 拉起,因此拉起的 shell 即 root。
//
// 纯 Go,交叉编译 linux/arm64 即可在本机(已验证 dxandroid-egress 同样方式运行)。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listenAddr := flag.String("listen", envOr("DXCTL_LISTEN", "10.66.0.101:22"), "监听地址(默认仅隧道 IP)")
	hostKeyPath := flag.String("hostkey", envOr("DXCTL_HOSTKEY", "/data/adb/dxandroid/keys/ssh_host_ed25519_key"), "主机私钥路径(不存在则生成)")
	authPath := flag.String("authorized", envOr("DXCTL_AUTHORIZED", "/data/adb/dxandroid/.ssh/authorized_keys"), "授权公钥文件")
	shellPath := flag.String("shell", envOr("DXCTL_SHELL", "/system/bin/sh"), "登录 shell")
	freebind := flag.Bool("freebind", true, "用 IP_FREEBIND 允许绑定尚未就绪的隧道 IP")
	flag.Parse()

	log.SetFlags(log.LstdFlags)

	signer, err := loadOrCreateHostKey(*hostKeyPath)
	if err != nil {
		log.Fatalf("host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			authorized, err := loadAuthorizedKeys(*authPath) // 每次连接时读,改公钥免重启
			if err != nil {
				return nil, fmt.Errorf("read authorized_keys: %w", err)
			}
			if authorized[string(key.Marshal())] {
				return &ssh.Permissions{Extensions: map[string]string{"pubkey-fp": ssh.FingerprintSHA256(key)}}, nil
			}
			return nil, fmt.Errorf("unauthorized key from %s", conn.RemoteAddr())
		},
	}
	cfg.AddHostKey(signer)

	ln, err := listen(*listenAddr, *freebind)
	if err != nil {
		log.Fatalf("listen %s: %v", *listenAddr, err)
	}
	log.Printf("dxandroid-control listening on %s (pubkey-only)", *listenAddr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, cfg, *shellPath)
	}
}

// listen 用 IP_FREEBIND 绑定,使得即便隧道 IP 暂未出现在接口上也能 bind。
func listen(addr string, freebind bool) (net.Listener, error) {
	lc := net.ListenConfig{}
	if freebind {
		lc.Control = func(network, address string, c syscall.RawConn) error {
			var ctrlErr error
			err := c.Control(func(fd uintptr) {
				ctrlErr = setFreebind(fd) // 平台相关:Linux 设 IP_FREEBIND,其它平台空操作
			})
			if err != nil {
				return err
			}
			return ctrlErr
		}
	}
	return lc.Listen(nil, "tcp", addr)
}

func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if data, err := os.ReadFile(path); err == nil {
		key, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse existing host key: %w", err)
		}
		return key, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "dxandroid-control")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	log.Printf("generated host key at %s", path)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	return signer, nil
}

func loadAuthorizedKeys(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	rest := data
	for len(rest) > 0 {
		pub, _, _, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break // 忽略注释/空行/无法解析的尾部
		}
		keys[string(pub.Marshal())] = true
		rest = r
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no usable keys in %s", path)
	}
	return keys, nil
}

func handleConn(nConn net.Conn, cfg *ssh.ServerConfig, shellPath string) {
	defer nConn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		log.Printf("handshake from %s failed: %v", nConn.RemoteAddr(), err)
		return
	}
	log.Printf("login from %s fp=%s", sshConn.RemoteAddr(), sshConn.Permissions.Extensions["pubkey-fp"])
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only session supported")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleSession(ch, chReqs, shellPath)
	}
}

type ptyReq struct {
	term string
	w, h uint32
}

func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, shellPath string) {
	defer ch.Close()
	var pr *ptyReq
	var ptyFile *os.File

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			term, w, h := parsePtyReq(req.Payload)
			pr = &ptyReq{term: term, w: w, h: h}
			req.Reply(true, nil)

		case "window-change":
			w, h := parseWinChange(req.Payload)
			if ptyFile != nil {
				pty.Setsize(ptyFile, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
			}

		case "env":
			req.Reply(true, nil) // 接受但不强制透传

		case "shell", "exec":
			var command string
			if req.Type == "exec" {
				command = parseExec(req.Payload)
			}
			f, err := startCommand(ch, pr, shellPath, command)
			if err != nil {
				req.Reply(false, nil)
				ch.Close()
				return
			}
			ptyFile = f
			req.Reply(true, nil)
			// startCommand 内部已起 goroutine 等待并发送 exit-status

		default:
			req.Reply(false, nil)
		}
	}
}

// startCommand 启动 shell。有 pty-req 时分配 PTY(交互式),否则用管道。
// 返回 pty 文件(用于后续 window-change 调整大小),无 pty 时返回 nil。
func startCommand(ch ssh.Channel, pr *ptyReq, shellPath, command string) (*os.File, error) {
	var cmd *exec.Cmd
	if command == "" {
		cmd = exec.Command(shellPath)
	} else {
		cmd = exec.Command(shellPath, "-c", command)
	}
	cmd.Env = append(os.Environ(),
		"HOME=/data/adb/dxandroid",
		"PATH=/system/bin:/system/xbin:/sbin:/vendor/bin",
	)

	if pr != nil {
		cmd.Env = append(cmd.Env, "TERM="+pr.term)
		f, err := pty.Start(cmd)
		if err != nil {
			return nil, err
		}
		pty.Setsize(f, &pty.Winsize{Rows: uint16(pr.h), Cols: uint16(pr.w)})
		go func() { io.Copy(f, ch) }()      // ch -> pty (stdin)
		go func() { io.Copy(ch, f) }()      // pty -> ch (stdout+stderr)
		go waitAndClose(cmd, ch, f)
		return f, nil
	}

	// 无 pty:分离 stdin/stdout/stderr
	stdin, _ := cmd.StdinPipe()
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { io.Copy(stdin, ch); stdin.Close() }()
	go waitAndClose(cmd, ch, nil)
	return nil, nil
}

func waitAndClose(cmd *exec.Cmd, ch ssh.Channel, f *os.File) {
	err := cmd.Wait()
	if f != nil {
		f.Close()
	}
	var status uint32
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				status = uint32(ws.ExitStatus())
			} else {
				status = 1
			}
		} else {
			status = 1
		}
	}
	ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
	ch.Close()
}

// --- SSH 请求 payload 解析 ---

func parsePtyReq(p []byte) (term string, w, h uint32) {
	term, p = readString(p)
	w, p = readUint32(p)
	h, _ = readUint32(p)
	return
}

func parseWinChange(p []byte) (w, h uint32) {
	w, p = readUint32(p)
	h, _ = readUint32(p)
	return
}

func parseExec(p []byte) string {
	cmd, _ := readString(p)
	return cmd
}

func readString(p []byte) (string, []byte) {
	if len(p) < 4 {
		return "", nil
	}
	n := binary.BigEndian.Uint32(p)
	p = p[4:]
	if uint32(len(p)) < n {
		return "", nil
	}
	return string(p[:n]), p[n:]
}

func readUint32(p []byte) (uint32, []byte) {
	if len(p) < 4 {
		return 0, nil
	}
	return binary.BigEndian.Uint32(p), p[4:]
}
