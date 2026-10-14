package webfill

import (
	"context"
	"reflect"
	"testing"
	"x-gateway/internal/model"
)

type fakeDriver struct {
	origin  string
	calls   []string
	missing string
	enabled bool
}

func (d *fakeDriver) current(context.Context) (Document, error) {
	d.calls = append(d.calls, "current")
	return Document{URL: d.origin + "/login", ID: "doc"}, nil
}
func (d *fakeDriver) visible(_ context.Context, s string) error {
	d.calls = append(d.calls, "visible:"+s)
	if s == d.missing || s == "#submit" && !d.enabled {
		return model.Err("PAGE_OPERATION_FAILED", "不可用")
	}
	return nil
}
func (d *fakeDriver) fill(_ context.Context, s, v string, doc Document) error {
	d.calls = append(d.calls, "fill:"+s)
	if s == "#password" {
		d.enabled = true
	}
	return nil
}
func (d *fakeDriver) click(_ context.Context, s string, doc Document) error {
	d.calls = append(d.calls, "click:"+s)
	return nil
}
func opts() Options {
	return Options{TargetID: "target", Origin: "https://example.test", UsernameSelector: "#username", PasswordSelector: "#password", Username: "name", Password: "canary"}
}
func TestFillDefaultDoesNotSubmitOrClear(t *testing.T) {
	d := &fakeDriver{origin: "https://example.test"}
	result, e := run(context.Background(), d, opts())
	if e != nil || !result.Filled || result.Submitted {
		t.Fatal(result, e)
	}
	want := []string{"current", "visible:#username", "visible:#password", "fill:#username", "fill:#password"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Fatal(d.calls)
	}
}
func TestMissingFieldFailsBeforeAnyMutation(t *testing.T) {
	d := &fakeDriver{origin: "https://example.test", missing: "#password"}
	_, e := run(context.Background(), d, opts())
	if e == nil {
		t.Fatal("missing field accepted")
	}
	if !reflect.DeepEqual(d.calls, []string{"current", "visible:#username", "visible:#password"}) {
		t.Fatal(d.calls)
	}
}
func TestSubmitEnabledByFillIsClickedOnce(t *testing.T) {
	d := &fakeDriver{origin: "https://example.test"}
	o := opts()
	o.SubmitSelector = "#submit"
	result, e := run(context.Background(), d, o)
	if e != nil || !result.Submitted {
		t.Fatal(result, e)
	}
	want := []string{"current", "visible:#username", "visible:#password", "fill:#username", "fill:#password", "visible:#submit", "click:#submit"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Fatal(d.calls)
	}
}
func TestSingleFieldAndPreciseOrigin(t *testing.T) {
	for _, field := range []string{"username", "password"} {
		d := &fakeDriver{origin: "https://example.test"}
		o := opts()
		if field == "username" {
			o.PasswordSelector = ""
		} else {
			o.UsernameSelector = ""
		}
		result, e := run(context.Background(), d, o)
		if e != nil || !result.Filled || len(d.calls) != 3 {
			t.Fatal(result, e, d.calls)
		}
	}
	for _, origin := range []string{"http://example.test", "https://example.test:8443", "https://other.test"} {
		d := &fakeDriver{origin: origin}
		_, e := run(context.Background(), d, opts())
		if e == nil || len(d.calls) != 1 {
			t.Fatal("cross-origin mutation", origin, d.calls)
		}
	}
}
func TestOriginNormalizesDefaultPorts(t *testing.T) {
	for input, want := range map[string]string{"https://EXAMPLE.test:443/path?q=secret": "https://example.test", "http://localhost:80/login": "http://localhost", "http://localhost:00080/login": "http://localhost", "http://localhost:00123/login": "http://localhost:123", "http://localhost:123/login": "http://localhost:123", "https://[0:0:0:0:0:0:0:1]:00443/path": "https://[::1]"} {
		got, e := Origin(input)
		if e != nil || got != want {
			t.Fatal(input, got, e)
		}
	}
}

func TestCanonicalIPv6OriginGuard(t *testing.T) {
	d := &fakeDriver{origin: "https://[0:0:0:0:0:0:0:1]:00443"}
	o := opts()
	o.Origin = "https://[::1]"
	if _, e := run(context.Background(), d, o); e != nil {
		t.Fatal("equivalent IPv6 origin rejected", e)
	}
	d = &fakeDriver{origin: "https://[::1]:8443"}
	if _, e := run(context.Background(), d, o); e == nil || len(d.calls) != 1 {
		t.Fatal("different IPv6 port allowed mutation")
	}
}

func TestMappedIPv6CannotUseIPv4CredentialOrigin(t *testing.T) {
	d := &fakeDriver{origin: "http://[::ffff:127.0.0.1]"}
	o := opts()
	o.Origin = "http://127.0.0.1"
	if _, e := run(context.Background(), d, o); e == nil || len(d.calls) != 1 {
		t.Fatal("mapped IPv6 accepted IPv4 credential origin")
	}
	d = &fakeDriver{origin: "http://[::ffff:7f00:1]"}
	o.Origin = "http://[::ffff:7f00:1]"
	if _, e := run(context.Background(), d, o); e != nil {
		t.Fatal("canonical mapped IPv6 rejected", e)
	}
}
