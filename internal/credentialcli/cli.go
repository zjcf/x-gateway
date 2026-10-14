// Package credentialcli provides the shared, daemon-free credential writer.
package credentialcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"io"
	"os"
	"strconv"
	"unicode/utf8"
	"x-gateway/internal/localstore"
	"x-gateway/internal/model"
)

type secretInput struct {
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	PrivateKey     string `json:"private_key,omitempty"`
	Passphrase     string `json:"passphrase,omitempty"`
	EnablePassword string `json:"enable_password,omitempty"`
	SudoPassword   string `json:"sudo_password,omitempty"`
}
type response struct {
	OK        bool         `json:"ok"`
	Operation string       `json:"operation"`
	Result    any          `json:"result,omitempty"`
	Error     *model.Error `json:"error,omitempty"`
}
type webMetadata struct {
	Name     string `json:"name"`
	Origin   string `json:"origin"`
	Username string `json:"username"`
}

func webView(row localstore.Metadata) webMetadata {
	return webMetadata{Name: row.Name, Origin: row.Origin, Username: row.Username}
}

func emit(out io.Writer, op string, result any, e error) int {
	r := response{OK: e == nil, Operation: op, Result: result}
	if e != nil {
		r.Result = nil
		var safe *model.Error
		if errors.As(e, &safe) {
			r.Error = safe
		} else {
			r.Error = model.Err("OPERATION_FAILED", "操作失败")
		}
	}
	if json.NewEncoder(out).Encode(r) != nil {
		return 1
	}
	if e != nil {
		return 1
	}
	return 0
}
func Run(ctx context.Context, kind, dir string, args []string, in *os.File, out, diag io.Writer) int {
	if kind != "ssh" && kind != "web" {
		return emit(out, "credential", nil, model.Err("INVALID_CREDENTIAL", "未知凭据类型"))
	}
	if len(args) == 0 {
		return emit(out, "credential", nil, model.Err("INVALID_ARGUMENT", "需要credential add/list/delete"))
	}
	action := args[0]
	pos := []string{}
	url := ""
	stdin := false
	port := 0
	user := ""
	userSet := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--stdin":
			stdin = true
		case "--url", "--port", "-p", "--data-dir", "--user":
			flag := args[i]
			i++
			if i >= len(args) {
				return emit(out, "credential", nil, model.Err("INVALID_ARGUMENT", "参数缺少值"))
			}
			switch flag {
			case "--user":
				if userSet || args[i] == "" {
					return emit(out, "credential", nil, model.Err("INVALID_ARGUMENT", "--user需要唯一非空用户名"))
				}
				user = args[i]
				userSet = true
			case "--url":
				url = args[i]
			case "--data-dir":
				dir = args[i]
			default:
				n, e := strconv.Atoi(args[i])
				if e != nil || n < 1 || n > 65535 {
					return emit(out, "credential", nil, model.Err("INVALID_ARGUMENT", "端口无效"))
				}
				port = n
			}
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return emit(out, "credential", nil, model.Err("INVALID_ARGUMENT", "未知参数"))
			}
			pos = append(pos, args[i])
		}
	}
	op := "credential." + action
	if kind == "web" && port != 0 {
		return emit(out, op, nil, model.Err("INVALID_ARGUMENT", "网站凭据不接受SSH端口参数"))
	}
	if userSet && (kind != "web" || action != "delete") {
		return emit(out, op, nil, model.Err("INVALID_ARGUMENT", "--user仅用于网站凭据delete"))
	}
	if action == "list" || action == "rotate" {
		if len(pos) != 0 || stdin || url != "" || port != 0 {
			return emit(out, op, nil, model.Err("INVALID_ARGUMENT", "无效参数"))
		}
	} else if (action != "add" && action != "delete") || len(pos) != 1 {
		return emit(out, op, nil, model.Err("INVALID_ARGUMENT", "需要一个目标"))
	}
	target := ""
	if len(pos) > 0 {
		target = pos[0]
	}
	if kind == "ssh" && target != "" {
		var e error
		_, e = localstore.ParseSSH(target, port)
		if e != nil {
			return emit(out, op, nil, e)
		}
	}
	if action == "delete" && (stdin || url != "") {
		return emit(out, op, nil, model.Err("INVALID_ARGUMENT", "delete不接受秘密输入"))
	}
	r := localstore.Record{Kind: kind, Target: target, Name: target, URL: url}
	if action == "add" {
		if kind == "ssh" {
			address, e := localstore.ParseSSH(target, port)
			if e != nil {
				return emit(out, op, nil, e)
			}
			if address.ExplicitPort {
				r.Port = address.Port
			}
			r.Target = address.Target()
			target = r.Target
		}
		if kind == "web" {
			origin, e := localstore.WebOrigin(url)
			if e != nil {
				return emit(out, op, nil, e)
			}
			r.Origin = origin
		}
		var input secretInput
		var e error
		if stdin {
			input, e = readSecret(ctx, kind, in, diag)
		} else {
			input, e = prompt(ctx, kind, in, diag)
		}
		if e != nil {
			return emit(out, op, nil, e)
		}
		r.Username = input.Username
		r.Password = input.Password
		r.PrivateKey = input.PrivateKey
		r.Passphrase = input.Passphrase
		r.EnablePassword = input.EnablePassword
		r.SudoPassword = input.SudoPassword
	}
	s, e := localstore.Open(dir)
	if e != nil {
		return emit(out, op, nil, e)
	}
	switch action {
	case "add":
		e = s.Put(r)
		if kind == "web" {
			return emit(out, op, webMetadata{Name: target, Origin: r.Origin, Username: r.Username}, e)
		}
		return emit(out, op, map[string]any{"kind": kind, "target": target, "origin": r.Origin, "saved": true}, e)
	case "delete":
		if kind == "ssh" {
			e = s.DeleteSSH(target, port)
		} else {
			var row localstore.Metadata
			if userSet {
				row, e = s.DeleteWeb(target, user)
			} else {
				row, e = s.DeleteWeb(target)
			}
			return emit(out, op, webView(row), e)
		}
		return emit(out, op, map[string]any{"kind": kind, "target": target, "deleted": true}, e)
	case "list":
		rows, e := s.List(kind)
		if kind == "web" {
			result := make([]webMetadata, 0, len(rows))
			for _, row := range rows {
				result = append(result, webView(row))
			}
			return emit(out, op, result, e)
		}
		return emit(out, op, rows, e)
	case "rotate":
		return emit(out, "key.rotate", map[string]bool{"rotated": true}, s.RotateKey())
	}
	return 1
}
func prompt(ctx context.Context, kind string, in *os.File, diag io.Writer) (secretInput, error) {
	var out secretInput
	if !term.IsTerminal(int(in.Fd())) {
		return out, model.Err("TTY_REQUIRED", "人工录入需要TTY；受控管道请使用--stdin")
	}
	read := func(label string) (string, error) {
		fmt.Fprint(diag, label+": ")
		state, e := term.MakeRaw(int(in.Fd()))
		if e != nil {
			return "", model.Err("INPUT_FAILED", "无法关闭终端回显")
		}
		defer term.Restore(int(in.Fd()), state)
		b, e := readBytes(ctx, in, true, true)
		fmt.Fprintln(diag)
		defer clear(b)
		if e != nil {
			return "", e
		}
		return string(b), nil
	}
	var e error
	if kind == "web" {
		out.Username, e = read("用户名")
		if e != nil {
			return out, e
		}
	}
	out.Password, e = read("密码（私钥请使用--stdin导入）")
	return out, e
}
func readSecret(ctx context.Context, kind string, in *os.File, diag io.Writer) (secretInput, error) {
	var out secretInput
	tty := term.IsTerminal(int(in.Fd()))
	if tty {
		state, e := term.MakeRaw(int(in.Fd()))
		if e != nil {
			return out, model.Err("TTY_REQUIRED", "无法关闭终端回显")
		}
		defer term.Restore(int(in.Fd()), state)
		if e = json.NewEncoder(diag).Encode(map[string]any{"operation": "credential.add", "ready": true, "input": "hidden_json_line"}); e != nil {
			return out, model.Err("INPUT_FAILED", "无法通知输入就绪")
		}
	} else {
		stat, e := in.Stat()
		if e != nil || stat.Mode().IsRegular() {
			return out, model.Err("INVALID_CREDENTIAL", "秘密输入仅接受管道或隐藏TTY，不接受文件")
		}
	}
	b, e := readBytes(ctx, in, tty, false)
	defer clear(b)
	if e != nil {
		return out, e
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if kind == "web" {
		var web struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if decoder.Decode(&web) != nil {
			return out, model.Err("INVALID_CREDENTIAL", "网站JSON只允许username/password")
		}
		out.Username = web.Username
		out.Password = web.Password
	} else if decoder.Decode(&out) != nil {
		return out, model.Err("INVALID_CREDENTIAL", "SSH凭据JSON字段无效")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return out, model.Err("INVALID_CREDENTIAL", "凭据JSON包含多余内容")
	}
	return out, nil
}
func readBytes(ctx context.Context, in *os.File, line, edit bool) ([]byte, error) {
	b := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	defer clear(buf)
	fds := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
	for {
		if ctx.Err() != nil {
			return b, model.Err("INPUT_CANCELLED", "凭据输入已取消")
		}
		n, e := unix.Poll(fds, 100)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return b, model.Err("INPUT_FAILED", "秘密输入失败")
		}
		if n == 0 {
			continue
		}
		n, e = unix.Read(int(in.Fd()), buf)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return b, model.Err("INPUT_FAILED", "秘密输入失败")
		}
		if n == 0 {
			if line {
				return b, model.Err("INVALID_CREDENTIAL", "需要一行完整JSON")
			}
			return b, nil
		}
		for _, c := range buf[:n] {
			if edit && (c == 127 || c == 8) {
				if len(b) > 0 {
					_, size := utf8.DecodeLastRune(b)
					clear(b[len(b)-size:])
					b = b[:len(b)-size]
				}
				continue
			}
			if edit && c == 21 {
				clear(b)
				b = b[:0]
				continue
			}
			if line && (c == 3 || c == 4) {
				return b, model.Err("INPUT_CANCELLED", "凭据输入已取消")
			}
			if line && (c == '\r' || c == '\n') {
				return b, nil
			}
			b = append(b, c)
			if len(b) > 1<<20 {
				return b, model.Err("INVALID_CREDENTIAL", "秘密JSON超过1MiB")
			}
		}
	}
}
