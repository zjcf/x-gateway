// simple-web-fixture is a loopback-only explicit-field test site. It has no
// credential store, daemon, browser installation or production configuration.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const fakeUsername = "fixture-user"
const fakePassword = "Fixture-Only-测试-'\"-123!"

type site struct{ submissions, successes atomic.Int64 }

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch r.URL.Path {
	case "/login", "/multi", "/disabled":
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		multi := r.URL.Path == "/multi"
		disabled := ""
		if r.URL.Path == "/disabled" {
			disabled = " disabled"
		}
		fmt.Fprint(w, `<!doctype html><html><meta charset="utf-8"><title>xweb 假凭据填写测试</title><h1>xweb 隔离测试</h1><form method="post" action="/auth"><label id="username-row">用户名 <input id="username" name="username" autocomplete="off"></label>`)
		if multi {
			fmt.Fprint(w, `<button id="next" type="button">下一步</button><div id="password-row" hidden>`)
		} else {
			fmt.Fprint(w, `<div id="password-row">`)
		}
		fmt.Fprintf(w, `<label>密码 <input id="password" name="password" type="password" autocomplete="off"%s></label><button id="submit" type="submit" disabled>登录</button></div><input id="ue" name="ue" type="hidden"><input id="pe" name="pe" type="hidden"></form>`, disabled)
		fmt.Fprint(w, `<script>
const u=document.querySelector('#username');
function enableSubmit(){document.querySelector('#submit').disabled=!(u.value&&document.querySelector('#password').value);}
function bindPassword(){const p=document.querySelector('#password');for(const type of ['input','change'])p.addEventListener(type,()=>{const h=document.querySelector('#pe');h.value+=(h.value?',':'')+type;enableSubmit();});}
bindPassword();
for(const type of ['input','change'])u.addEventListener(type,()=>{
 const h=document.querySelector('#ue');h.value+=(h.value?',':'')+type;
 // Simulate React replacing the password field during a username update.
 const old=document.querySelector('#password');const fresh=old.cloneNode(true);old.replaceWith(fresh);bindPassword();enableSubmit();
});
document.querySelector('#next')?.addEventListener('click',()=>{
 document.querySelector('#username-row').hidden=true;
 document.querySelector('#next').hidden=true;
 document.querySelector('#password-row').hidden=false;
});
</script></html>`)
	case "/auth":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		s.submissions.Add(1)
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.ParseForm() != nil || r.PostForm.Get("username") != fakeUsername || r.PostForm.Get("password") != fakePassword || r.PostForm.Get("ue") != "input,change" || r.PostForm.Get("pe") != "input,change" {
			http.Error(w, "测试提交未通过", 401)
			return
		}
		s.successes.Add(1)
		http.Redirect(w, r, "/done", 303)
	case "/done":
		fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><h1 id="done">测试提交成功</h1>`)
	case "/status":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"submissions": s.submissions.Load(), "successes": s.successes.Load()})
	default:
		http.NotFound(w, r)
	}
}
func main() {
	ttl := flag.Duration("ttl", 30*time.Minute, "测试期限")
	hostname := flag.String("hostname", "localhost", "localhost或127.0.0.1")
	flag.Parse()
	if (*hostname != "localhost" && *hostname != "127.0.0.1") || *ttl <= 0 {
		fmt.Fprintln(os.Stderr, "测试参数无效")
		os.Exit(1)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		fmt.Fprintln(os.Stderr, "无法监听测试端口")
		os.Exit(1)
	}
	defer listener.Close()
	server := &http.Server{Handler: &site{}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	defer server.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *ttl)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	origin := "http://" + net.JoinHostPort(*hostname, fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	json.NewEncoder(os.Stdout).Encode(map[string]string{"login_url": origin + "/login", "multi_url": origin + "/multi", "disabled_url": origin + "/disabled", "status_url": origin + "/status"})
	select {
	case <-ctx.Done():
	case <-done:
	}
}
