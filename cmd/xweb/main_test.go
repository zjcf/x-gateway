package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"x-gateway/internal/localstore"
)

func TestUnknownSecretFlagIsNotReflected(t *testing.T) {
	var out, diag bytes.Buffer
	code := run(context.Background(), []string{"fill", "test", "--password", "canary-secret"}, os.Stdin, &out, &diag)
	if code == 0 || strings.Contains(out.String(), "canary-secret") {
		t.Fatal(code, out.String())
	}
}

func TestHelpAliases(t *testing.T) {
	if !strings.Contains(help, "fill SITE") || strings.Contains(help, "fill TARGET") || !strings.Contains(help, "--target-id ID") || !strings.Contains(help, "origin是网站地址") {
		t.Fatal("help conflates site metadata and browser tab ID")
	}
	for _, alias := range []string{"-h", "--help", "help"} {
		var out, diag bytes.Buffer
		if code := run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "unused"), alias}, os.Stdin, &out, &diag); code != 0 || out.String() != help || diag.Len() != 0 {
			t.Fatal("help aliases differ")
		}
	}
}

func TestAccountSelectionFailsBeforeAnyBrowserCall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	store, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, user := range []string{"alice", "bob"} {
		if e = store.Put(localstore.Record{Kind: "web", Target: "site", URL: "https://example.com", Username: user, Password: "secret-canary"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		args []string
		code string
	}{{nil, "CREDENTIAL_AMBIGUOUS"}, {[]string{"--user", "missing"}, "CREDENTIAL_NOT_FOUND"}, {[]string{"--user", ""}, "INVALID_ARGUMENT"}} {
		var out, diag bytes.Buffer
		args := []string{"--data-dir", dir, "fill", "site", "--cdp", "not-a-browser-endpoint", "--password-selector", "#password"}
		args = append(args, tc.args...)
		if n := run(context.Background(), args, os.Stdin, &out, &diag); n == 0 || !strings.Contains(out.String(), tc.code) || strings.Contains(out.String(), "secret-canary") {
			t.Fatal("account selection did not stop before browser")
		}
	}
}

func TestAddressSelectionFailsBeforeAnyBrowserCall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []localstore.Record{{Kind: "web", Target: "10.0.0.1", Username: "alice", Password: "secret-canary", URL: "http://10.0.0.1"}, {Kind: "web", Target: "alias", Username: "bob", Password: "secret-canary", URL: "http://10.0.0.1"}} {
		if e = s.Put(r); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct{ target, user, code string }{{"10.0.0.1", "", "CREDENTIAL_AMBIGUOUS"}, {"http://10.0.0.1/path", "missing", "CREDENTIAL_NOT_FOUND"}} {
		var out, diag bytes.Buffer
		args := []string{"--data-dir", dir, "fill", tc.target, "--cdp", "not-a-browser-endpoint", "--password-selector", "#password"}
		if tc.user != "" {
			args = append(args, "--user", tc.user)
		}
		if n := run(context.Background(), args, os.Stdin, &out, &diag); n == 0 || !strings.Contains(out.String(), tc.code) || strings.Contains(out.String(), "secret-canary") {
			t.Fatal("address selection failed to stop before browser")
		}
	}
}

func TestExactNameFlagIsBooleanAndFillOnly(t *testing.T) {
	pos, flags, e := parse([]string{"fill", "--exact-name", "10.0.0.1", "--user", "alice"})
	if e != nil || len(pos) != 2 || pos[1] != "10.0.0.1" || flags["exact-name"] != "true" || flags["user"] != "alice" {
		t.Fatal("exact-name consumed a value")
	}
	if _, _, e = parse([]string{"fill", "site", "--exact-name", "--exact-name"}); e == nil {
		t.Fatal("duplicate exact-name accepted")
	}
	for _, args := range [][]string{{"targets", "--exact-name"}, {"native-host", "--exact-name"}, {"native-manifest", "--exact-name"}, {"credential", "list", "--exact-name"}} {
		var out, diag bytes.Buffer
		if n := run(context.Background(), args, os.Stdin, &out, &diag); n == 0 {
			t.Fatal("non-fill accepted exact-name")
		}
	}
	dir := filepath.Join(t.TempDir(), "data")
	s, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []localstore.Record{{Kind: "web", Target: "10.0.0.1", Username: "alice", Password: "secret-canary", URL: "https://other.example"}, {Kind: "web", Target: "alias", Username: "bob", Password: "secret-canary", URL: "http://10.0.0.1"}} {
		if e = s.Put(r); e != nil {
			t.Fatal(e)
		}
	}
	var out, diag bytes.Buffer
	args := []string{"--data-dir", dir, "fill", "--exact-name", "10.0.0.1", "--user", "bob", "--cdp", "not-a-browser-endpoint", "--password-selector", "#password"}
	if n := run(context.Background(), args, os.Stdin, &out, &diag); n == 0 || !strings.Contains(out.String(), "CREDENTIAL_NOT_FOUND") {
		t.Fatal("exact-name fell back to address account")
	}
}
func TestManifestKeepsExistingExtensionIDAndQuotesMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "space ' directory")
	data, e := manifest(dir, "ipehodkodnkhfjpeipcibkgpogiebfoj", "/private/tmp/xweb bin")
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil || m["name"] != "com.x_gateway.accessctl" {
		t.Fatal(string(data))
	}
	script, e := os.ReadFile(m["path"].(string))
	if e != nil || !strings.Contains(string(script), "native-host \"$@\"") {
		t.Fatal(e, string(script))
	}
	info, _ := os.Stat(m["path"].(string))
	if info.Mode().Perm() != 0700 {
		t.Fatal(info.Mode())
	}
}
