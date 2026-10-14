package localstore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"x-gateway/internal/model"
)

func TestBareSSHHostSelectionAndSafeCandidates(t *testing.T) {
	s, _ := openTest(t)
	if e := s.Put(Record{Kind: "ssh", Target: "alice@HOST", Port: 2200, Password: "secret-canary"}); e != nil {
		t.Fatal(e)
	}
	r, e := s.GetSSH("host", 0)
	if e != nil || r.Username != "alice" || r.Port != 2200 {
		t.Fatal("bare unique host failed")
	}
	for _, r := range []Record{{Kind: "ssh", Target: "alice@host", Password: "default-secret"}, {Kind: "ssh", Target: "bob@host", Password: "other-secret"}} {
		if e = s.Put(r); e != nil {
			t.Fatal(e)
		}
	}
	for _, port := range []int{0, 22} {
		_, e = s.GetSSH("HOST", port)
		requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
		if !strings.Contains(e.Error(), `["alice","bob"]`) || !strings.Contains(e.Error(), "user@host") || strings.Contains(e.Error(), "secret") {
			t.Fatal("unsafe or incomplete account candidates")
		}
	}
	r, e = s.GetSSH("host", 2200)
	if e != nil || r.Username != "alice" {
		t.Fatal("explicit port did not filter accounts")
	}
	_, e = s.GetSSH("alice@host", 0)
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	if !strings.Contains(e.Error(), "[22,2200]") || !strings.Contains(e.Error(), "-p PORT") || strings.Contains(e.Error(), "bob") {
		t.Fatal("port candidates invalid")
	}
	_, e = s.GetSSH("missing@host", 22)
	requireCode(t, e, "CREDENTIAL_NOT_FOUND")
	_, e = s.GetSSH("host:2200", 22)
	requireCode(t, e, "INVALID_TARGET")
	requireCode(t, s.DeleteSSH("host", 2200), "INVALID_TARGET")
	_, e = s.Get("ssh", "host")
	requireCode(t, e, "INVALID_TARGET")
	rows, _ := s.List("ssh")
	if len(rows) != 3 {
		t.Fatal("bare host delete changed records")
	}
}
func TestBareIPv6Selection(t *testing.T) {
	s, _ := openTest(t)
	if e := s.Put(Record{Kind: "ssh", Target: "demo@[::1]:2200", Password: "secret"}); e != nil {
		t.Fatal(e)
	}
	for _, target := range []string{"::1", "[::1]", "[::1]:2200"} {
		r, e := s.GetSSH(target, 0)
		if e != nil || r.Target != "demo@[::1]:2200" {
			t.Fatal("bare IPv6 lookup failed")
		}
	}
}
func TestWebAmbiguousUsersAreJSONEncodedAndSorted(t *testing.T) {
	s, _ := openTest(t)
	users := []string{"z-user", "a\"comma,\x1b", "middle"}
	for _, user := range users {
		r := testRecord("site")
		r.Username = user
		if e := s.Put(r); e != nil {
			t.Fatal(e)
		}
	}
	_, e := s.Get("web", "site")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	var safe *model.Error
	errors.As(e, &safe)
	want, _ := json.Marshal([]string{users[1], users[2], users[0]})
	if !strings.Contains(safe.Message, string(want)) || !strings.Contains(safe.Message, "--user USER") || strings.Contains(safe.Message, "\x1b") || strings.Contains(safe.Message, "secret-canary") {
		t.Fatal("unsafe or unsorted usernames")
	}
}
