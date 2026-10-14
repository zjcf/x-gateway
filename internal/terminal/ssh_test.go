package terminal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"x-gateway/scripts/simple-ssh-fixture/server"
)

func fixture(t *testing.T) (Config, *server.Server) {
	t.Helper()
	s, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	host, p, _ := net.SplitHostPort(s.Address())
	port, _ := strconv.Atoi(p)
	return Config{Host: host, Port: port, Username: server.Username, Password: server.Password, DataDir: privateTemp(t)}, s
}

func privateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPTYRawBytesControlResizeExitAndReconnect(t *testing.T) {
	c, _ := fixture(t)
	for attempt := 0; attempt < 2; attempt++ {
		var out, diag bytes.Buffer
		s, err := connect(context.Background(), c, &out, &diag, 24, 80)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.remote.WindowChange(41, 113); err != nil {
			t.Fatal(err)
		}
		_, err = io.WriteString(s.input, "unfinished\x03whoami\rshow status\rshow size\ransi\rexit 7\r")
		if err != nil {
			t.Fatal(err)
		}
		code, err := s.wait()
		s.close()
		if err != nil || code != 7 {
			t.Fatalf("exit=%d err=%v", code, err)
		}
		for _, want := range []string{"^C\r\ndemo> ", "demo\r\n", "status: ready\r\n", "size: 41 113\r\n", "\x1b[32m你好 UTF-8\x1b[0m\r\n"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing %q in %q", want, out.String())
			}
		}
		if (attempt == 0) != strings.Contains(diag.String(), "TOFU:") {
			t.Fatalf("unexpected TOFU notice: %q", diag.String())
		}
	}
}

func TestDisconnectAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			c, _ := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := connect(ctx, c, io.Discard, io.Discard, 24, 80)
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if cancelled {
				cancel()
			} else {
				_, _ = io.WriteString(s.input, "disconnect\r")
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				code, err := s.wait()
				if code != 255 || err == nil {
					t.Errorf("exit=%d err=%v", code, err)
				}
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("session did not close")
			}
		})
	}
}

func TestHandshakeCancellation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := l.Accept()
		if e == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	host, p, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(p)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = connect(ctx, Config{Host: host, Port: port, Username: "demo", Password: "canary", DataDir: privateTemp(t)}, io.Discard, io.Discard, 24, 80)
	if err == nil || !strings.Contains(err.Error(), "SSH_TIMEOUT") {
		t.Fatalf("got %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handshake conn leaked")
	}
}

func TestAuthenticationErrorsDoNotExposeCredential(t *testing.T) {
	c, _ := fixture(t)
	c.Password = "AUTH_SECRET_CANARY"
	_, err := connect(context.Background(), c, io.Discard, io.Discard, 24, 80)
	if err == nil || strings.Contains(err.Error(), c.Password) {
		t.Fatalf("unsafe error %v", err)
	}
}

func TestChangedHostKeyRejectsReconnect(t *testing.T) {
	c, first := fixture(t)
	s, err := connect(context.Background(), c, io.Discard, io.Discard, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(s.input, "exit\r")
	_, err = s.wait()
	s.close()
	if err != nil {
		t.Fatal(err)
	}
	address := first.Address()
	first.Close()
	second, err := server.Start(address)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Fingerprint == first.Fingerprint {
		t.Fatal("fixture reused key")
	}
	_, err = connect(context.Background(), c, io.Discard, io.Discard, 24, 80)
	if err == nil || !strings.Contains(err.Error(), "HOST_KEY_CHANGED") {
		t.Fatalf("changed key accepted: %v", err)
	}
}

func publicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestTOFUConcurrentPinAndChanged(t *testing.T) {
	dir := privateTemp(t)
	key, other := publicKey(t), publicKey(t)
	cb, err := hostKeyCallback(dir, "[::1]:2222", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := cb("", nil, key); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if err = cb("", nil, other); err == nil || !strings.Contains(err.Error(), "HOST_KEY_CHANGED") {
		t.Fatalf("changed key accepted %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "simple", "known_hosts"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("pins=%v err=%v", entries, err)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if err = cb("", nil, key); err != nil {
		t.Fatal("changed check altered pin", err)
	}
}

func TestTOFURejectsSymlink(t *testing.T) {
	dir := privateTemp(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "simple")); err != nil {
		t.Fatal(err)
	}
	if _, err := hostKeyCallback(dir, "localhost:22", io.Discard); err == nil {
		t.Fatal("accepted directory symlink")
	}
	dir = privateTemp(t)
	cb, err := hostKeyCallback(dir, "localhost:22", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "simple", "known_hosts"))
	_ = entries
	key := publicKey(t)
	if err = cb("", nil, key); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(filepath.Join(dir, "simple", "known_hosts"))
	pin := filepath.Join(dir, "simple", "known_hosts", entries[0].Name())
	if err = os.Remove(pin); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(t.TempDir(), "missing"), pin); err != nil {
		t.Fatal(err)
	}
	if err = cb("", nil, key); err == nil {
		t.Fatal("accepted file symlink")
	}
}
