package localstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putWeb(t *testing.T, s *Store, name, user, rawURL string) {
	t.Helper()
	if e := s.Put(Record{Kind: "web", Target: name, Username: user, Password: "secret-canary", URL: rawURL}); e != nil {
		t.Fatal(e)
	}
}
func TestWebAddressLookupAndAccountSelection(t *testing.T) {
	s, _ := openTest(t)
	putWeb(t, s, "10.0.0.1", "alice", "http://10.0.0.1/login")
	putWeb(t, s, "alias", "bob", "http://10.0.0.1/other")
	for _, target := range []string{"10.0.0.1", "10.0.0.1:80", "http://10.0.0.1/ignored?password=secret-canary#ignored"} {
		_, e := s.ResolveWeb(target)
		requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
		if !strings.Contains(e.Error(), `["alice","bob"]`) || strings.Contains(e.Error(), "secret-canary") {
			t.Fatal("IP name shadowed alias or leaked")
		}
		r, e := s.ResolveWeb(target, "bob")
		if e != nil || r.Target != "alias" || r.Origin != "http://10.0.0.1" {
			t.Fatal("address account selection failed")
		}
	}
	r, e := s.Get("web", "10.0.0.1")
	if e != nil || r.Username != "alice" {
		t.Fatal("Get exact-name semantics changed")
	}
	_, e = s.ResolveWeb("10.0.0.1", "missing")
	requireCode(t, e, "CREDENTIAL_NOT_FOUND")
	putWeb(t, s, "unrelated-ip-name", "eve", "http://10.0.0.2")
	_, e = s.ResolveWeb("10.0.0.3")
	requireCode(t, e, "CREDENTIAL_NOT_FOUND")
}
func TestExplicitAddressesDoNotFallbackToSameStringName(t *testing.T) {
	s, _ := openTest(t)
	putWeb(t, s, "10.0.0.9", "alice", "https://elsewhere.example")
	putWeb(t, s, "https://target.example/login", "bob", "https://elsewhere.example")
	for _, target := range []string{"10.0.0.9", "https://target.example/login"} {
		_, e := s.ResolveWeb(target)
		requireCode(t, e, "CREDENTIAL_NOT_FOUND")
	}
	putWeb(t, s, "domain.example", "carol", "https://elsewhere.example")
	putWeb(t, s, "domain-alias", "dave", "https://domain.example")
	r, e := s.ResolveWeb("domain.example")
	if e != nil || r.Username != "carol" {
		t.Fatal("ordinary name priority changed")
	}
	r, e = s.ResolveWeb("https://domain.example/path")
	if e != nil || r.Username != "dave" {
		t.Fatal("URL origin lookup failed")
	}
}
func TestWebBareHostRequiresUnambiguousOrigin(t *testing.T) {
	s, _ := openTest(t)
	putWeb(t, s, "http", "alice", "http://example.com")
	putWeb(t, s, "https", "bob", "https://example.com/login")
	putWeb(t, s, "custom", "carol", "https://example.com:8443")
	_, e := s.ResolveWeb("EXAMPLE.COM", "alice")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	if !strings.Contains(e.Error(), `["http://example.com","https://example.com","https://example.com:8443"]`) || !strings.Contains(e.Error(), "完整HTTP(S) URL") {
		t.Fatal("origin ambiguity guessed from user")
	}
	for _, tc := range []struct{ target, user string }{{"example.com:80", "alice"}, {"example.com:443", "bob"}, {"example.com:8443", "carol"}, {"https://example.com:443/any?x=y", "bob"}} {
		r, e := s.ResolveWeb(tc.target)
		if e != nil || r.Username != tc.user {
			t.Fatal("effective port filter failed")
		}
	}
	putWeb(t, s, "https80", "dave", "https://example.com:80")
	_, e = s.ResolveWeb("example.com:80")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
}
func TestWebDuplicateAliasAccountRequiresName(t *testing.T) {
	s, _ := openTest(t)
	for _, name := range []string{"z-alias", "a-alias"} {
		putWeb(t, s, name, "same-user", "https://example.com/login")
	}
	for _, user := range [][]string{nil, {"same-user"}} {
		_, e := s.ResolveWeb("https://example.com", user...)
		requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
		if !strings.Contains(e.Error(), `["a-alias","z-alias"]`) || !strings.Contains(e.Error(), "--exact-name") {
			t.Fatal("duplicate alias chosen or wrong recovery")
		}
	}
	r, e := s.ResolveWeb("a-alias")
	if e != nil || r.Target != "a-alias" {
		t.Fatal("exact alias not selectable")
	}
	putWeb(t, s, "third", "different-user", "https://example.com")
	_, e = s.ResolveWeb("https://example.com")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
	if !strings.Contains(e.Error(), `["different-user","same-user"]`) {
		t.Fatal("candidate usernames wrong")
	}
}

func TestWebOriginCanonicalIPAndNumericPorts(t *testing.T) {
	s, _ := openTest(t)
	r := Record{Kind: "web", Target: "old-ipv6", Username: "alice", Password: "secret", URL: "https://[0:0:0:0:0:0:0:1]:00443/login"}
	before := writeLegacy(t, s, []Record{r})
	for _, target := range []string{"https://[::1]/different", "https://[0:0:0:0:0:0:0:1]:443/", "[::1]:0443", "::1"} {
		got, e := s.ResolveWeb(target)
		if e != nil || got.Origin != "https://[::1]" {
			t.Fatal("legacy origin canonicalization failed")
		}
	}
	after, _ := os.ReadFile(filepath.Join(s.dir, "credentials.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("normalization rewrote store")
	}
	putWeb(t, s, "alias", "bob", "https://[::1]")
	_, e := s.ResolveWeb("https://[0:0:0:0:0:0:0:1]:0443")
	requireCode(t, e, "CREDENTIAL_AMBIGUOUS")
}

func TestMappedIPv6KeepsDistinctWebOrigin(t *testing.T) {
	mapped, e := WebOrigin("http://[::ffff:127.0.0.1]:00080/login")
	if e != nil || mapped != "http://[::ffff:7f00:1]" {
		t.Fatal("mapped IPv6 serialization differs from browser")
	}
	ipv4, e := WebOrigin("http://127.0.0.1")
	if e != nil || mapped == ipv4 {
		t.Fatal("mapped IPv6 merged with IPv4")
	}
	s, _ := openTest(t)
	putWeb(t, s, "v4", "alice", "http://127.0.0.1")
	putWeb(t, s, "mapped", "bob", "http://[::ffff:127.0.0.1]")
	for _, tc := range []struct{ target, user string }{{"127.0.0.1", "alice"}, {"::ffff:127.0.0.1", "bob"}, {"[::ffff:7f00:1]:80", "bob"}, {"http://[::ffff:7f00:1]/login", "bob"}} {
		r, e := s.ResolveWeb(tc.target)
		if e != nil || r.Username != tc.user {
			t.Fatal("mapped host lookup confused address families")
		}
	}
}
func TestWebIPv6AndLegacyLookupIsReadOnly(t *testing.T) {
	s, _ := openTest(t)
	r := Record{Kind: "web", Target: "old", Username: "alice", Password: "secret", URL: "http://[::1]:2200/login"}
	before := writeLegacy(t, s, []Record{r})
	for _, target := range []string{"::1", "[::1]", "[::1]:2200", "http://[::1]:2200/other"} {
		got, e := s.ResolveWeb(target)
		if e != nil || got.Target != "old" || got.Origin != "http://[::1]:2200" {
			t.Fatal("IPv6 lookup failed")
		}
	}
	after, _ := os.ReadFile(filepath.Join(s.dir, "credentials.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("lookup rewrote legacy ciphertext")
	}
	for _, target := range []string{"[::1]:bad", "example.com:0", "http://user:pass@example.com", "name/path"} {
		_, e := s.ResolveWeb(target)
		requireCode(t, e, "INVALID_TARGET")
	}
	_, e := s.ResolveWeb("old", "")
	requireCode(t, e, "INVALID_ARGUMENT")
	_, e = s.ResolveWeb("old", "a", "b")
	requireCode(t, e, "INVALID_ARGUMENT")
}
