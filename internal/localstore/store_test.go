package localstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testRecord(name string) Record {
	return Record{Kind: "web", Target: name, URL: "https://example.com/login", Username: "user-canary", Password: "secret-canary"}
}
func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	return s, dir
}
func TestAutomaticEncryptionRotationAndPermissions(t *testing.T) {
	s, dir := openTest(t)
	r := testRecord("one")
	if e := s.Put(r); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "simple", "credentials.json")
	before, _ := os.ReadFile(path)
	machine, _ := os.ReadFile(filepath.Join(dir, "simple", "machine.key"))
	for _, v := range []string{r.Username, r.Password, r.URL} {
		if bytes.Contains(before, []byte(v)) {
			t.Fatal("plaintext in storage")
		}
	}
	for _, name := range []string{"credentials.json", "machine.key", ".lock"} {
		info, e := os.Stat(filepath.Join(dir, "simple", name))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe permissions")
		}
	}
	if e := s.RotateKey(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	nextMachine, _ := os.ReadFile(filepath.Join(dir, "simple", "machine.key"))
	if bytes.Equal(before, after) || !bytes.Equal(machine, nextMachine) {
		t.Fatal("rotation contract")
	}
	reopened, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	got, e := reopened.Get("web", "one")
	if e != nil || got.Password != r.Password {
		t.Fatal("rotation lost record")
	}
	rows, _ := s.List("")
	b, _ := json.Marshal(rows)
	if !bytes.Contains(b, []byte(r.Username)) || bytes.Contains(b, []byte(r.Password)) {
		t.Fatal("metadata leaked")
	}
}
func TestMissingMachineKeyNeverRegenerated(t *testing.T) {
	s, dir := openTest(t)
	s.Put(testRecord("one"))
	key := filepath.Join(dir, "simple", "machine.key")
	os.Remove(key)
	if _, e := Open(dir); e == nil {
		t.Fatal("missing key accepted")
	}
	if _, e := os.Stat(key); !os.IsNotExist(e) {
		t.Fatal("key regenerated")
	}
}
func TestTamperAndUnknownKeyReject(t *testing.T) {
	for _, mode := range []string{"ciphertext", "nonce", "key-id", "wrong-machine", "record-id"} {
		t.Run(mode, func(t *testing.T) {
			s, dir := openTest(t)
			s.Put(testRecord("one"))
			path := filepath.Join(dir, "simple", "credentials.json")
			b, _ := os.ReadFile(path)
			var d document
			json.Unmarshal(b, &d)
			for id, entry := range d.Records {
				switch mode {
				case "ciphertext":
					entry.Data[0] ^= 1
				case "nonce":
					entry.Nonce = []byte{1}
				case "key-id":
					entry.KeyID = "unknown"
				case "record-id":
					delete(d.Records, id)
					id = "wrong"
				}
				d.Records[id] = entry
				break
			}
			b, _ = json.Marshal(d)
			os.WriteFile(path, b, 0600)
			if mode == "wrong-machine" {
				os.WriteFile(filepath.Join(dir, "simple", "machine.key"), make([]byte, 32), 0600)
			}
			if _, e := Open(dir); e == nil {
				t.Fatal("corrupt store accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(b, after) {
				t.Fatal("corrupt store overwritten")
			}
		})
	}
}
func TestEmptySSHRejected(t *testing.T) {
	s, _ := openTest(t)
	if e := s.Put(Record{Kind: "ssh", Target: "user@host"}); e == nil {
		t.Fatal("empty auth accepted")
	}
}
func TestConcurrentProcessWriter(t *testing.T) {
	if os.Getenv("XG_TEST_WRITER") == "1" {
		s, e := Open(os.Getenv("XG_TEST_DIR"))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Put(testRecord(os.Getenv("XG_TEST_TARGET"))); e != nil {
			t.Fatal(e)
		}
		return
	}
	s, dir := openTest(t)
	cmds := []*exec.Cmd{}
	for i := 0; i < 12; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestConcurrentProcessWriter$")
		c.Env = append(os.Environ(), "XG_TEST_WRITER=1", "XG_TEST_DIR="+dir, fmt.Sprintf("XG_TEST_TARGET=target-%d", i))
		if e := c.Start(); e != nil {
			t.Fatal(e)
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		if e := c.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := s.List("web")
	if e != nil || len(rows) != 12 {
		t.Fatal("concurrent writes lost")
	}
}
func TestAtomicRenameFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "destination")
	os.Mkdir(path, 0700)
	os.WriteFile(filepath.Join(path, "marker"), []byte("unchanged"), 0600)
	if e := atomicWrite(path, []byte("replacement")); e == nil {
		t.Fatal("rename unexpectedly succeeded")
	}
	b, e := os.ReadFile(filepath.Join(path, "marker"))
	if e != nil || string(b) != "unchanged" {
		t.Fatal("failed rename changed destination")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".credential-*"))
	if len(matches) != 0 {
		t.Fatal("temporary files leaked")
	}
}
