package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"x-gateway/internal/localstore"
)

func capturedRun(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr; rOut.Close(); rErr.Close() }()
	code := run(context.Background(), args)
	wOut.Close()
	wErr.Close()
	out, _ := io.ReadAll(rOut)
	diag, _ := io.ReadAll(rErr)
	return code, string(out), string(diag)
}

func TestHelpAliases(t *testing.T) {
	for _, alias := range []string{"-h", "--help", "help"} {
		code, out, diag := capturedRun(t, []string{"--data-dir", filepath.Join(t.TempDir(), "unused"), alias})
		if code != 0 || out != help || diag != "" {
			t.Fatal("help aliases differ")
		}
	}
}

func TestBareHostSelectionErrorsBeforeConnection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []localstore.Record{{Kind: "ssh", Target: "alice@127.0.0.1:22", Password: "secret-canary"}, {Kind: "ssh", Target: "bob@127.0.0.1:22", Password: "secret-canary"}, {Kind: "ssh", Target: "alice@127.0.0.1:2200", Password: "secret-canary"}} {
		if e = s.Put(r); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct{ target, expected string }{{"127.0.0.1", `["alice","bob"]`}, {"alice@127.0.0.1", "[22,2200]"}, {"missing@127.0.0.1", "CREDENTIAL_NOT_FOUND"}} {
		code, _, diag := capturedRun(t, []string{"--data-dir", dir, tc.target})
		if code == 0 || !strings.Contains(diag, tc.expected) || strings.Contains(diag, "secret-canary") {
			t.Fatal("selection failed to stop before connection")
		}
	}
}

func TestParseTarget(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		target string
		port   int
	}{{[]string{"user@host"}, "user@host", 0}, {[]string{"user@host", "-p", "2222"}, "user@host", 2222}, {[]string{"-p", "2222", "user@[::1]"}, "user@[::1]", 2222}} {
		target, port, err := parseTarget(tc.args)
		if err != nil || target != tc.target || port != tc.port {
			t.Fatalf("%v: %s %d %v", tc.args, target, port, err)
		}
	}
	for _, args := range [][]string{{}, {"user@host", "-p"}, {"user@host", "-p", "0"}, {"user@host", "extra"}, {"user@host", "-L", "127.0.0.1:8888"}} {
		if _, _, err := parseTarget(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
