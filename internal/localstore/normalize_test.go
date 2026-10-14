package localstore

import "testing"

func TestCanonicalTargets(t *testing.T) {
	if _, e := NormalizeSSH("u@host:2222", 22); e == nil {
		t.Fatal("explicit22 conflict accepted")
	}
	for raw, want := range map[string]string{"u@EXAMPLE.COM": "u@example.com:22", "u@[::1]:2200": "u@[::1]:2200", "u@::1": "u@[::1]:22"} {
		got, e := NormalizeSSH(raw, 0)
		if e != nil || got != want {
			t.Fatalf("normalization %q", raw)
		}
	}
	for _, raw := range []string{"missing-user", "u x@host", "u@host:bad", "u@../host", "u@host:0"} {
		if _, e := NormalizeSSH(raw, 0); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for raw, want := range map[string]string{"https://EXAMPLE.COM:443/a?q=x": "https://example.com", "http://localhost:1234/login": "http://localhost:1234", "https://[::1]/": "https://[::1]"} {
		got, e := WebOrigin(raw)
		if e != nil || got != want {
			t.Fatalf("origin %q", raw)
		}
	}
	for _, raw := range []string{"http://:80/", "https://u:p@example.com", "file:///tmp/test", "https://example.com:99999"} {
		if _, e := WebOrigin(raw); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
