package localstore

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"x-gateway/internal/model"
)

type SSHAddress struct {
	Username, Host string
	Port           int
	ExplicitPort   bool
}

func (a SSHAddress) Target() string {
	return a.Username + "@" + net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
}
func NormalizeSSH(target string, port int) (string, error) {
	a, e := ParseSSH(target, port)
	if e != nil {
		return "", e
	}
	return a.Target(), nil
}

// ParseSSHConnection allows a bare host only for credential lookup on login.
func ParseSSHConnection(target string, port int) (SSHAddress, error) {
	if strings.Contains(target, "@") {
		return ParseSSH(target, port)
	}
	a, e := ParseSSH("lookup@"+target, port)
	a.Username = ""
	return a, e
}
func ParseSSH(target string, port int) (SSHAddress, error) {
	bad := func() (SSHAddress, error) {
		return SSHAddress{}, model.Err("INVALID_TARGET", "SSH目标必须为user@host[:port]，显式端口不能冲突")
	}
	user, host, ok := strings.Cut(target, "@")
	if !ok || user == "" || host == "" || strings.ContainsAny(user, "@/\\\x00\r\n \t") {
		return bad()
	}
	explicit := port != 0
	if port < 0 || port > 65535 {
		return bad()
	}
	if h, p, e := net.SplitHostPort(host); e == nil {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 || explicit && port != n {
			return bad()
		}
		host = h
		port = n
		explicit = true
	} else {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return bad()
		}
	}
	if host == "" || strings.ContainsAny(host, "@/\\\x00\r\n \t") {
		return bad()
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	if port == 0 {
		port = 22
	}
	return SSHAddress{Username: user, Host: host, Port: port, ExplicitPort: explicit}, nil
}

func WebOrigin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", model.Err("INVALID_TARGET", "网站URL必须为无用户信息的HTTP或HTTPS地址")
	}
	host := strings.ToLower(u.Hostname())
	host = canonicalHost(host)
	if host == "" || strings.ContainsAny(host, "\x00\r\n \t") {
		return "", model.Err("INVALID_TARGET", "网站主机无效")
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", model.Err("INVALID_TARGET", "网站端口无效")
		}
		port = strconv.Itoa(n)
		if u.Scheme == "http" && n == 80 || u.Scheme == "https" && n == 443 {
			port = ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return strings.ToLower(u.Scheme) + "://" + host, nil
}
