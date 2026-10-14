package credentialcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"x-gateway/internal/localstore"
)

func invoke(t *testing.T, kind, dir, payload string, args ...string) (int, string) {
	t.Helper()
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	go func() { defer w.Close(); w.WriteString(payload) }()
	var out, diag bytes.Buffer
	n := Run(context.Background(), kind, dir, args, r, &out, &diag)
	return n, out.String() + diag.String()
}

func assertWebView(t *testing.T, raw json.RawMessage, name, origin, user string) {
	t.Helper()
	var row map[string]any
	if json.Unmarshal(raw, &row) != nil || len(row) != 3 || row["name"] != name || row["origin"] != origin || row["username"] != user {
		t.Fatal("web result must contain only name/origin/username")
	}
}
func TestWebPublicProjectionAndSelectedDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	for _, user := range []string{"alice", "bob"} {
		n, out := invoke(t, "web", dir, `{"username":"`+user+`","password":"secret-canary"}`, "add", "site", "--url", "https://example.com:443/login", "--stdin")
		if n != 0 || strings.Contains(out, "secret-canary") {
			t.Fatal("add failed or leaked")
		}
		var envelope struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal([]byte(out), &envelope) != nil || !envelope.OK {
			t.Fatal("invalid envelope")
		}
		assertWebView(t, envelope.Result, "site", "https://example.com", user)
	}
	n, out := invoke(t, "web", dir, "", "list")
	if n != 0 {
		t.Fatal("list failed")
	}
	var list struct {
		Result []json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(out), &list) != nil || len(list.Result) != 2 {
		t.Fatal("invalid list")
	}
	assertWebView(t, list.Result[0], "site", "https://example.com", "alice")
	assertWebView(t, list.Result[1], "site", "https://example.com", "bob")
	n, out = invoke(t, "web", dir, "", "delete", "site", "--user", "alice")
	if n != 0 {
		t.Fatal("selected delete failed")
	}
	var deleted struct {
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal([]byte(out), &deleted)
	assertWebView(t, deleted.Result, "site", "https://example.com", "alice")
	n, out = invoke(t, "web", dir, "", "delete", "site")
	if n != 0 {
		t.Fatal("unique delete failed")
	}
	json.Unmarshal([]byte(out), &deleted)
	assertWebView(t, deleted.Result, "site", "https://example.com", "bob")
	n, out = invoke(t, "web", dir, "", "list")
	if n != 0 || !strings.Contains(out, `"result":[]`) {
		t.Fatal("empty list changed shape")
	}
}
func TestSSHProjectionUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	n, out := invoke(t, "ssh", dir, `{"password":"secret-canary"}`, "add", "user@host", "--stdin")
	if n != 0 {
		t.Fatal("add failed")
	}
	var added struct {
		Result map[string]any `json:"result"`
	}
	json.Unmarshal([]byte(out), &added)
	if added.Result["kind"] != "ssh" || added.Result["target"] != "user@host:22" || added.Result["saved"] != true {
		t.Fatal("SSH add projection changed")
	}
	n, out = invoke(t, "ssh", dir, "", "list")
	if n != 0 {
		t.Fatal("list failed")
	}
	var list struct {
		Result []map[string]any `json:"result"`
	}
	json.Unmarshal([]byte(out), &list)
	if len(list.Result) != 1 || list.Result[0]["id"] == nil || list.Result[0]["kind"] != "ssh" || list.Result[0]["target"] != "user@host:22" || list.Result[0]["host"] != "host" {
		t.Fatal("SSH list projection changed")
	}
}
func TestPasswordOnlySSHAndStrictWeb(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	n, out := invoke(t, "ssh", dir, `{"password":"secret-canary"}`, "add", "user@HOST", "--stdin")
	if n != 0 || strings.Contains(out, "secret-canary") {
		t.Fatal("write failed or leaked")
	}
	s, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Get("ssh", "user@host")
	if e != nil || r.Username != "user" || r.Password != "secret-canary" {
		t.Fatal("password-only SSH failed")
	}
	for _, p := range []string{`{"username":"u","password":"secret-canary","private_key":""}`, `{"username":"u","password":"secret-canary","target":"other"}`, `{"username":"u","password":"secret-canary"} {}`} {
		n, out = invoke(t, "web", dir, p, "add", "site", "--url", "https://example.com", "--stdin")
		if n == 0 || strings.Contains(out, "secret-canary") {
			t.Fatal("strict schema failed")
		}
	}
	n, out = invoke(t, "ssh", dir, `{}`, "add", "user@host", "--stdin")
	if n == 0 {
		t.Fatal("empty auth overwrote saved record")
	}
	r, _ = s.Get("ssh", "user@host")
	if r.Password != "secret-canary" {
		t.Fatal("failed input overwrote record")
	}
	n, out = invoke(t, "ssh", dir, "", "list")
	if n != 0 || strings.Contains(out, "secret-canary") {
		t.Fatal("list leaked")
	}
	n, out = invoke(t, "ssh", dir, "", "rotate")
	if n != 0 {
		t.Fatal(out)
	}
}
func TestRejectRegularFileAndOversize(t *testing.T) {
	file, e := os.CreateTemp(t.TempDir(), "input")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	file.WriteString(`{"password":"secret-canary"}`)
	file.Seek(0, 0)
	var out bytes.Buffer
	if n := Run(context.Background(), "ssh", filepath.Join(t.TempDir(), "data"), []string{"add", "u@h", "--stdin"}, file, &out, &out); n == 0 || strings.Contains(out.String(), "secret-canary") {
		t.Fatal("regular secret file accepted")
	}
	n, output := invoke(t, "ssh", filepath.Join(t.TempDir(), "data"), strings.Repeat("a", (1<<20)+1), "add", "u@h", "--stdin")
	if n == 0 || len(output) > 2048 {
		t.Fatal("oversize input mishandled")
	}
}
func TestCancelledPipe(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, e := readSecret(ctx, "ssh", r, &bytes.Buffer{})
	if e == nil || time.Since(start) > time.Second {
		t.Fatal("cancel blocked")
	}
}

func TestManualEditingAndJSONBytesPreserved(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		edit        bool
	}{
		{"passx\x7fword\r", "password", true},
		{"passx\bword\n", "password", true},
		{"discard\x15password\r", "password", true},
		{"密错\x7f码\r", "密码", true},
		{"\x7f\bpassword\r", "password", true},
		{"pass\x7f\b\x15word\n", "pass\x7f\b\x15word", false},
	} {
		r, w, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		w.WriteString(tc.input)
		w.Close()
		got, e := readBytes(context.Background(), r, true, tc.edit)
		r.Close()
		if e != nil || string(got) != tc.want {
			t.Fatal("hidden input editing contract failed")
		}
		clear(got)
	}
}

func TestMultipleWebAccountsAndSafeDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	for _, action := range []string{"add", "delete"} {
		n, out := invoke(t, "web", dir, "", action, "site", "-p", "22")
		if n == 0 || !strings.Contains(out, "INVALID_ARGUMENT") {
			t.Fatal("web accepted SSH port flag")
		}
	}
	for _, user := range []string{"alice", "bob"} {
		n, out := invoke(t, "web", dir, `{"username":"`+user+`","password":"secret-canary"}`, "add", "site", "--url", "https://example.com/login", "--stdin")
		if n != 0 || strings.Contains(out, "secret-canary") {
			t.Fatal("add failed or leaked")
		}
	}
	n, out := invoke(t, "web", dir, "", "delete", "site")
	if n == 0 || !strings.Contains(out, "CREDENTIAL_AMBIGUOUS") {
		t.Fatal("ambiguous delete accepted")
	}
	n, out = invoke(t, "web", dir, "", "list")
	if n != 0 || !strings.Contains(out, "alice") || !strings.Contains(out, "bob") || strings.Contains(out, "secret-canary") {
		t.Fatal("account metadata invalid")
	}
	n, _ = invoke(t, "web", dir, "", "delete", "site", "--user", "")
	if n == 0 {
		t.Fatal("empty user accepted")
	}
	n, _ = invoke(t, "web", dir, "", "delete", "site", "--user", "alice")
	if n != 0 {
		t.Fatal("selected delete failed")
	}
	s, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Get("web", "site")
	if e != nil || r.Username != "bob" {
		t.Fatal("selected delete affected other account")
	}
}

func TestSSHStoredPortAndDeleteSelection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	n, _ := invoke(t, "ssh", dir, `{"password":"secret-canary"}`, "add", "user@host", "-p", "2222", "--stdin")
	if n != 0 {
		t.Fatal("explicit port add failed")
	}
	s, e := localstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.GetSSH("user@host", 0)
	if e != nil || r.Port != 2222 {
		t.Fatal("stored port absent")
	}
	n, _ = invoke(t, "ssh", dir, `{"password":"secret-canary"}`, "add", "user@host:2222", "-p", "22", "--stdin")
	if n == 0 {
		t.Fatal("port conflict accepted")
	}
	n, _ = invoke(t, "ssh", dir, `{"password":"secret-canary"}`, "add", "user@host", "--stdin")
	if n != 0 {
		t.Fatal("default port add failed")
	}
	n, out := invoke(t, "ssh", dir, "", "delete", "user@host")
	if n == 0 || !strings.Contains(out, "CREDENTIAL_AMBIGUOUS") {
		t.Fatal("ambiguous ssh delete accepted")
	}
	n, _ = invoke(t, "ssh", dir, "", "delete", "user@host", "-p", "22")
	if n != 0 {
		t.Fatal("exact ssh delete failed")
	}
	n, _ = invoke(t, "ssh", dir, "", "delete", "user@host")
	if n != 0 {
		t.Fatal("unique saved port delete failed")
	}
}
