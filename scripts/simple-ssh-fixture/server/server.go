// Package server 是仅供本地验收使用的假SSH服务；所有凭据均公开且不能用于真实设备。
package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

const Username = "demo"
const Password = "fake-xssh-password"

type Server struct {
	listener    net.Listener
	config      *ssh.ServerConfig
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
	wg          sync.WaitGroup
	Fingerprint string
}

func Start(address string) (*Server, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, err
	}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() != Username || string(p) != Password {
			return nil, fmt.Errorf("fixture authentication rejected")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	s := &Server{listener: listener, config: config, connections: map[net.Conn]bool{}, Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func (s *Server) Address() string { return s.listener.Addr().String() }

func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	_ = s.listener.Close()
	for c := range s.connections {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.connections[c] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() { _ = c.Close(); s.mu.Lock(); delete(s.connections, c); s.mu.Unlock() }()
			s.serve(c)
		}()
	}
}

func (s *Server) serve(c net.Conn) {
	conn, channels, requests, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	var sessions sync.WaitGroup
	defer sessions.Wait()
	for ch := range channels {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		channel, reqs, err := ch.Accept()
		if err != nil {
			continue
		}
		sessions.Add(1)
		go func() { defer sessions.Done(); serveSession(channel, reqs) }()
	}
}

func serveSession(ch ssh.Channel, requests <-chan *ssh.Request) {
	defer ch.Close()
	var mu sync.Mutex
	rows, cols := 24, 80
	started := false
	done := make(chan struct{})
	defer func() {
		if started {
			<-done
		}
	}()
	for r := range requests {
		switch r.Type {
		case "pty-req":
			var p struct {
				Term                      string
				Cols, Rows, Width, Height uint32
				Modes                     string
			}
			ok := ssh.Unmarshal(r.Payload, &p) == nil
			if ok {
				mu.Lock()
				rows, cols = int(p.Rows), int(p.Cols)
				mu.Unlock()
			}
			_ = r.Reply(ok, nil)
		case "window-change":
			var p struct{ Cols, Rows, Width, Height uint32 }
			ok := ssh.Unmarshal(r.Payload, &p) == nil
			if ok {
				mu.Lock()
				rows, cols = int(p.Rows), int(p.Cols)
				mu.Unlock()
			}
			_ = r.Reply(ok, nil)
		case "shell":
			if started {
				_ = r.Reply(false, nil)
				continue
			}
			started = true
			_ = r.Reply(true, nil)
			go func() {
				defer close(done)
				shell(ch, func() (int, int) { mu.Lock(); defer mu.Unlock(); return rows, cols })
			}()
		default:
			_ = r.Reply(false, nil)
		}
	}
}

func shell(ch ssh.Channel, size func() (int, int)) {
	_, _ = io.WriteString(ch, "fake SSH ready\r\ndemo> ")
	buf := make([]byte, 4096)
	line := []byte{}
	lastCR := false
	for {
		n, err := ch.Read(buf)
		if err != nil {
			return
		}
		for _, b := range buf[:n] {
			if b == 3 {
				line = nil
				_, _ = io.WriteString(ch, "^C\r\ndemo> ")
				lastCR = false
				continue
			}
			if b == 4 {
				if len(line) == 0 {
					exit(ch, 0)
					return
				}
				continue
			}
			if b == 127 || b == 8 {
				if len(line) > 0 {
					line = line[:len(line)-1]
					_, _ = io.WriteString(ch, "\b \b")
				}
				continue
			}
			if b == '\r' || b == '\n' {
				if b == '\n' && lastCR {
					lastCR = false
					continue
				}
				lastCR = b == '\r'
				_, _ = io.WriteString(ch, "\r\n")
				cmd := strings.TrimSpace(string(line))
				line = nil
				switch {
				case cmd == "whoami":
					_, _ = io.WriteString(ch, Username+"\r\n")
				case cmd == "show status":
					_, _ = io.WriteString(ch, "status: ready\r\n")
				case cmd == "show size":
					r, c := size()
					_, _ = fmt.Fprintf(ch, "size: %d %d\r\n", r, c)
				case cmd == "ansi":
					_, _ = io.WriteString(ch, "\x1b[32m你好 UTF-8\x1b[0m\r\n")
				case cmd == "disconnect":
					_ = ch.Close()
					return
				case cmd == "exit" || strings.HasPrefix(cmd, "exit "):
					code := 0
					if cmd != "exit" {
						code, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(cmd, "exit ")))
					}
					exit(ch, code)
					return
				case cmd != "":
					_, _ = io.WriteString(ch, "unknown fixture command\r\n")
				}
				_, _ = io.WriteString(ch, "demo> ")
				continue
			}
			lastCR = false
			line = append(line, b)
			_, _ = ch.Write([]byte{b})
		}
	}
}

func exit(ch ssh.Channel, code int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
	_ = ch.Close()
}
