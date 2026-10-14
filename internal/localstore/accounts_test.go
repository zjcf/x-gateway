package localstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"x-gateway/internal/model"
)

func requireCode(t *testing.T, e error, code string) {
	t.Helper()
	var safe *model.Error
	if !errors.As(e, &safe) || safe.Code != code {
		t.Fatalf("want %s, got %v", code, e)
	}
}

func TestDeleteWebReturnsExactCommittedAccount(t *testing.T) {
	s, _ := openTest(t)
	for _, user := range []string{"alice", "bob"} {
		r := testRecord("site")
		r.Username = user
		if e := s.Put(r); e != nil {
			t.Fatal(e)
		}
	}
	row, e := s.DeleteWeb("site")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	if row != (Metadata{}) {
		t.Fatal("failed delete returned metadata")
	}
	var wg sync.WaitGroup
	for _, user := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(user string) {
			defer wg.Done()
			row, e := s.DeleteWeb("site", user)
			if e != nil || row.Username != user || row.Name != "site" || row.Origin != "https://example.com" {
				t.Error("delete metadata does not match selected account")
			}
		}(user)
	}
	wg.Wait()
	rows, e := s.List("web")
	if e != nil || len(rows) != 0 {
		t.Fatal("delete did not commit exactly two accounts")
	}
	row, e = s.DeleteWeb("site", "alice")
	requireCode(t, e, "CREDENTIAL_NOT_FOUND")
	if row != (Metadata{}) {
		t.Fatal("missing delete returned metadata")
	}
}
func TestWebAccountsUpdateDeleteAndOrigin(t *testing.T) {
	s, _ := openTest(t)
	a := testRecord("site")
	a.Username = "Alice"
	b := a
	b.Username = "alice"
	b.Password = "other-secret"
	if e := s.Put(a); e != nil {
		t.Fatal(e)
	}
	if e := s.Put(b); e != nil {
		t.Fatal(e)
	}
	_, e := s.Get("web", "site")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	_, e = s.DeleteWeb("site")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	for _, r := range []Record{a, b} {
		got, e := s.Get("web", "site", r.Username)
		if e != nil || got.Password != r.Password {
			t.Fatal("account overwritten")
		}
	}
	a.Password = "updated-secret"
	if e = s.Put(a); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Get("web", "site", "alice")
	if got.Password != b.Password {
		t.Fatal("update changed other account")
	}
	conflict := a
	conflict.URL = "https://other.example/login"
	requireCode(t, s.Put(conflict), "CREDENTIAL_ORIGIN_CONFLICT")
	_, e = s.Get("web", "site", "missing")
	requireCode(t, e, "CREDENTIAL_NOT_FOUND")
	_, e = s.Get("web", "site", "")
	requireCode(t, e, "INVALID_ARGUMENT")
	_, e = s.Get("web", "site", "a", "b")
	requireCode(t, e, "INVALID_ARGUMENT")
	_, e = s.DeleteWeb("site", "a", "b")
	requireCode(t, e, "INVALID_ARGUMENT")
	if e = s.RotateKey(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteWeb("site", "Alice"); e != nil {
		t.Fatal(e)
	}
	got, e = s.Get("web", "site")
	if e != nil || got.Username != "alice" {
		t.Fatal("account delete affected other")
	}
	if e = s.Put(Record{Kind: "web", Target: "site", URL: "https://other.example/login", Username: "alice", Password: "new"}); e != nil {
		t.Fatal("single account URL update rejected")
	}
}
func TestSSHPortsAndAccounts(t *testing.T) {
	s, _ := openTest(t)
	if e := s.Put(Record{Kind: "ssh", Target: "alice@HOST", Port: 2222, Password: "first"}); e != nil {
		t.Fatal(e)
	}
	r, e := s.GetSSH("alice@host", 0)
	if e != nil || r.Port != 2222 || r.Target != "alice@host:2222" {
		t.Fatal("saved port not reused")
	}
	if e = s.Put(Record{Kind: "ssh", Target: "bob@host", Password: "second"}); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(Record{Kind: "ssh", Target: "alice@host", Password: "default"}); e != nil {
		t.Fatal(e)
	}
	_, e = s.GetSSH("alice@host", 0)
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	requireCode(t, s.DeleteSSH("alice@host", 0), "CREDENTIAL_AMBIGUOUS")
	r, e = s.GetSSH("alice@host", 22)
	if e != nil || r.Password != "default" || r.Port != 0 {
		t.Fatal("explicit default lookup failed")
	}
	if e = s.Put(Record{Kind: "ssh", Target: "alice@host", Port: 22, Password: "explicit"}); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.List("ssh")
	if len(rows) != 3 {
		t.Fatal("default22 duplicated")
	}
	r, _ = s.GetSSH("alice@host:22", 0)
	if r.Port != 22 || r.Password != "explicit" {
		t.Fatal("explicit22 metadata lost")
	}
	_, e = s.GetSSH("alice@host:2222", 22)
	requireCode(t, e, "INVALID_TARGET")
	if e = s.DeleteSSH("alice@host", 2222); e != nil {
		t.Fatal(e)
	}
	r, e = s.GetSSH("alice@host", 0)
	if e != nil || r.Password != "explicit" {
		t.Fatal("delete selected wrong port")
	}
	r, e = s.GetSSH("bob@host", 0)
	if e != nil || r.Password != "second" {
		t.Fatal("different user lost")
	}
}

// Write an authentic pre-multiaccount envelope, including the old ID and AAD.
func writeLegacy(t *testing.T, s *Store, records []Record) []byte {
	t.Helper()
	e := s.withLocked(func(state *loaded) error {
		state.doc.Records = map[string]sealed{}
		for _, r := range records {
			r.ID = recordID(r.Kind, r.Target)
			r.Host = ""
			r.Port = 0
			plain, _ := json.Marshal(r)
			entry, e := seal(state.ring.Key, r.ID, state.ring.ID, plain)
			clear(plain)
			if e != nil {
				return e
			}
			state.doc.Records[r.ID] = entry
		}
		b, _ := json.Marshal(state.doc)
		return atomicWrite(filepath.Join(s.dir, "credentials.json"), b)
	})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(s.dir, "credentials.json"))
	return b
}
func TestLegacyReadAndAtomicAccountUpgrade(t *testing.T) {
	for _, operation := range []string{"put", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			s, dir := openTest(t)
			a := testRecord("site")
			a.Username = "old-user"
			before := writeLegacy(t, s, []Record{a, {Kind: "ssh", Target: "demo@host:2200", Username: "demo", Password: "ssh-secret"}, {Kind: "ssh", Target: "other@host:22", Username: "other", Password: "default-secret"}})
			reopened, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			r, e := reopened.Get("web", "site")
			if e != nil || r.Username != a.Username {
				t.Fatal("legacy web inaccessible")
			}
			ssh, e := reopened.GetSSH("demo@host", 0)
			if e != nil || ssh.Port != 2200 {
				t.Fatal("legacy non22 not recovered")
			}
			ssh, e = reopened.GetSSH("other@host", 0)
			if e != nil || ssh.Port != 0 {
				t.Fatal("legacy22 metadata changed")
			}
			readOnly, _ := os.ReadFile(filepath.Join(s.dir, "credentials.json"))
			if !bytes.Equal(before, readOnly) {
				t.Fatal("read mutated legacy data")
			}
			if operation == "put" {
				b := a
				b.Username = "new-user"
				b.Password = "new-secret"
				e = reopened.Put(b)
			} else {
				e = reopened.RotateKey()
			}
			if e != nil {
				t.Fatal(e)
			}
			after, _ := os.ReadFile(filepath.Join(s.dir, "credentials.json"))
			var d document
			json.Unmarshal(after, &d)
			if _, exists := d.Records[recordID("web", "site")]; exists {
				t.Fatal("old ID retained during upgrade")
			}
			r, e = reopened.Get("web", "site", "old-user")
			if e != nil || r.Password != a.Password {
				t.Fatal("upgrade lost old account")
			}
			rows, _ := reopened.List("")
			want := 3
			if operation == "put" {
				want = 4
				_, e = reopened.Get("web", "site")
				requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
			}
			if len(rows) != want {
				t.Fatal("upgrade count wrong")
			}
		})
	}
}
