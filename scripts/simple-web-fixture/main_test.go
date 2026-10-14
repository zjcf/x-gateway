package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFixtureOnlyReturnsStatusAndRequiresInputChangeEvents(t *testing.T) {
	s := &site{}
	post := func(events string) *httptest.ResponseRecorder {
		body := url.Values{"username": {fakeUsername}, "password": {fakePassword}, "ue": {events}, "pe": {events}}.Encode()
		r := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), fakeUsername) || strings.Contains(w.Body.String(), fakePassword) {
			t.Fatal("credentials reflected")
		}
		return w
	}
	if post("").Code != http.StatusUnauthorized {
		t.Fatal("native input/change not required")
	}
	if post("input,change").Code != http.StatusSeeOther {
		t.Fatal("valid fake fill failed")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	var status map[string]int
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || status["submissions"] != 2 || status["successes"] != 1 || len(status) != 2 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), fakePassword) {
		t.Fatal("status exposed credential")
	}
}
func TestFixtureFormsInitiallyDisableSubmit(t *testing.T) {
	for _, path := range []string{"/login", "/multi", "/disabled"} {
		w := httptest.NewRecorder()
		(&site{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if !strings.Contains(w.Body.String(), `id="submit" type="submit" disabled`) {
			t.Fatal("empty submit enabled", path)
		}
	}
}
