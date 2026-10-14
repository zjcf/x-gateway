package localstore

import (
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"x-gateway/internal/model"
)

type webHost struct {
	host string
	port int
}

func canonicalHost(host string) string {
	if ip, e := netip.ParseAddr(host); e == nil {
		// WHATWG URL serialization uses hexadecimal IPv6, including mapped IPv4.
		if ip.Is4In6() {
			b := ip.As16()
			return "::ffff:" + strconv.FormatUint(uint64(b[12])<<8|uint64(b[13]), 16) + ":" + strconv.FormatUint(uint64(b[14])<<8|uint64(b[15]), 16)
		}
		return ip.String()
	}
	return strings.ToLower(host)
}
func parseWebHost(target string) (webHost, error) {
	bad := func() (webHost, error) {
		return webHost{}, model.Err("INVALID_TARGET", "网站目标需要名称、HTTP(S) URL或主机地址")
	}
	if target == "" || strings.ContainsAny(target, "/@\\?#\x00\r\n \t") {
		return bad()
	}
	if _, e := netip.ParseAddr(target); e == nil {
		return webHost{host: canonicalHost(target)}, nil
	}
	host, port := target, 0
	if h, p, e := net.SplitHostPort(target); e == nil {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return bad()
		}
		host, port = h, n
	} else if strings.HasPrefix(target, "[") && strings.HasSuffix(target, "]") {
		host = target[1 : len(target)-1]
		if net.ParseIP(host) == nil {
			return bad()
		}
	} else if strings.ContainsAny(target, "[]:") {
		return bad()
	}
	if host == "" {
		return bad()
	}
	return webHost{host: canonicalHost(host), port: port}, nil
}
func explicitWebAddress(target string) bool {
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return true
	}
	h, e := parseWebHost(target)
	return e == nil && net.ParseIP(h.host) != nil
}
func encodedSorted(set map[string]bool) string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	b, _ := json.Marshal(values)
	return string(b)
}

// ResolveWeb is a read-only login lookup. Get/Delete retain exact-name semantics.
func (s *Store) ResolveWeb(target string, user ...string) (Record, error) {
	var out Record
	e := s.withLocked(func(state *loaded) error { var e error; out, e = resolveWeb(state, target, user); return e })
	return out, e
}
func resolveWeb(state *loaded, target string, user []string) (Record, error) {
	if len(user) > 1 || len(user) == 1 && user[0] == "" {
		return Record{}, model.Err("INVALID_ARGUMENT", "用户选择必须为一个非空用户名")
	}
	if !explicitWebAddress(target) {
		for _, r := range state.records {
			if r.Kind == "web" && r.Target == target {
				return selectWeb(state, target, user)
			}
		}
	}
	matches := []Record{}
	if strings.Contains(target, "://") {
		origin, e := WebOrigin(target)
		if e != nil {
			return Record{}, e
		}
		for _, r := range state.records {
			if r.Kind == "web" && r.Origin == origin {
				matches = append(matches, r)
			}
		}
	} else {
		h, e := parseWebHost(target)
		if e != nil {
			return Record{}, e
		}
		origins := map[string]bool{}
		for _, r := range state.records {
			if r.Kind != "web" {
				continue
			}
			u, e := url.Parse(r.Origin)
			if e != nil {
				return Record{}, failure("STORE_CORRUPT")
			}
			port := 80
			if u.Scheme == "https" {
				port = 443
			}
			if u.Port() != "" {
				port, _ = strconv.Atoi(u.Port())
			}
			if canonicalHost(u.Hostname()) == h.host && (h.port == 0 || h.port == port) {
				matches = append(matches, r)
				origins[r.Origin] = true
			}
		}
		if len(origins) > 1 {
			return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该主机对应多个origin "+encodedSorted(origins)+"；请使用完整HTTP(S) URL指定目标")
		}
	}
	if len(user) == 1 {
		filtered := matches[:0]
		for _, r := range matches {
			if r.Username == user[0] {
				filtered = append(filtered, r)
			}
		}
		matches = filtered
	}
	if len(matches) > 1 {
		users := map[string]bool{}
		names := map[string]bool{}
		for _, r := range matches {
			users[r.Username] = true
			names[r.Target] = true
		}
		if len(users) > 1 {
			return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该网站有多个账号 "+encodedSorted(users)+"；请使用 --user USER 指定账号")
		}
		return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该账号对应多个网站名称 "+encodedSorted(names)+"；请使用具体名称并加 --exact-name 指定凭据")
	}
	return unique(matches)
}
