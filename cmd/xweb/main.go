package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"x-gateway/internal/credentialcli"
	"x-gateway/internal/localstore"
	"x-gateway/internal/model"
	"x-gateway/internal/webfill"
)

const help = `用法：
  xweb [全局选项] 命令 [参数]

日常命令：
  xweb fill SITE [--user USER] [--exact-name] --target-id ID [--username-selector CSS] [--password-selector CSS] [--submit-selector CSS]  # 在日常Chrome指定标签页填写凭据，可显式提交
  xweb fill SITE [--user USER] [--exact-name] --cdp URL [--target-id ID] [--username-selector CSS] [--password-selector CSS] [--submit-selector CSS]  # 在已有CDP页面填写凭据，可显式提交
  xweb targets [--cdp http://127.0.0.1:PORT]  # 查看日常Chrome或CDP的可用标签页
  xweb credential add NAME --url URL [--stdin]  # 保存或更新指定网站账号的凭据
  xweb credential list  # 查看网站名称、地址和用户名
  xweb credential delete NAME [--user USER]  # 删除指定网站账号的凭据
  xweb -h  # 显示帮助
  xweb --help  # 显示帮助
  xweb help  # 显示帮助

安装与维护命令：
  xweb credential rotate  # 轮换数据加密密钥，重新加密当前库全部SSH和Web凭据，不改账号密码
  xweb native-host  # 供Chrome自动调用，无需手动运行
  xweb native-manifest --extension-id ID [--binary ABS_PATH]  # 生成Chrome扩展连接配置

全局选项：
  --data-dir DIR  指定数据根目录

说明：
  SITE支持网站名称、HTTP(S) URL或主机/IP；IP和URL按地址查询，普通名称优先精确匹配。
  网站凭据结果仅含name、origin、username；origin是网站地址（协议、主机、端口）。
  主机对应多个origin时须使用完整URL；同账号有多个名称时须用具体名称加--exact-name。
  唯一账号时可省略--user；多个账号时须用--user USER明确选择，错误中列出已保存用户名。
  至少给一个用户名/密码selector。默认只填，不提交、不清空、不保存selector或流程。
  CDP未给target-id时，必须只有一个符合凭据origin的页面。
  无init、主口令、常驻serve或LaunchAgent；日常Chrome自动启动native-host。
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
func output(w io.Writer, operation string, result any, e error) int {
	var safe *model.Error
	if e != nil && !errors.As(e, &safe) {
		safe = model.Err("OPERATION_FAILED", "操作未完成")
	}
	_ = json.NewEncoder(w).Encode(struct {
		OK        bool         `json:"ok"`
		Operation string       `json:"operation"`
		Result    any          `json:"result,omitempty"`
		Error     *model.Error `json:"error,omitempty"`
	}{e == nil, operation, result, safe})
	if e == nil {
		return 0
	}
	if safe.Code == "INVALID_ARGUMENT" {
		return 2
	}
	return 1
}
func run(ctx context.Context, args []string, in *os.File, out, diag io.Writer) int {
	dir := localstore.DefaultDir()
	filtered := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--data-dir" {
			if i+1 >= len(args) {
				return output(out, "", nil, model.Err("INVALID_ARGUMENT", "data-dir缺少值"))
			}
			i++
			dir = args[i]
		} else {
			filtered = append(filtered, args[i])
		}
	}
	args = filtered
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return 0
	}
	if args[0] == "credential" {
		return credentialcli.Run(ctx, "web", dir, args[1:], in, out, diag)
	}
	if args[0] == "native-host" {
		if len(args) > 2 || len(args) == 2 && strings.HasPrefix(args[1], "--") {
			return output(diag, "native-host", nil, model.Err("INVALID_ARGUMENT", "native-host参数无效"))
		}
		e := webfill.NativeHost(ctx, dir, in, out)
		if e != nil {
			return output(diag, "native-host", nil, e)
		}
		return 0
	}
	pos, flags, e := parse(args)
	if e != nil {
		return output(out, "", nil, e)
	}
	op := "xweb." + pos[0]
	switch pos[0] {
	case "targets":
		if len(pos) != 1 || unknown(flags, "cdp") {
			return output(out, op, nil, model.Err("INVALID_ARGUMENT", "targets参数无效"))
		}
		child, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var targets []webfill.Target
		if flags["cdp"] != "" {
			targets, e = webfill.CDPTargets(child, flags["cdp"])
		} else {
			targets, e = webfill.Targets(child, dir)
		}
		return output(out, op, targets, e)
	case "fill":
		if len(pos) != 2 || unknown(flags, "cdp", "target-id", "username-selector", "password-selector", "submit-selector", "user", "exact-name") {
			return output(out, op, nil, model.Err("INVALID_ARGUMENT", "fill需要目标和selector"))
		}
		if flags["username-selector"] == "" && flags["password-selector"] == "" {
			return output(out, op, nil, model.Err("INVALID_ARGUMENT", "至少给一个用户名或密码selector"))
		}
		if flags["cdp"] == "" && flags["target-id"] == "" {
			return output(out, op, nil, model.Err("INVALID_ARGUMENT", "日常Chrome填写需要明确target-id"))
		}
		store, e := localstore.Open(dir)
		if e != nil {
			return output(out, op, nil, e)
		}
		var record localstore.Record
		if user, ok := flags["user"]; ok {
			if flags["exact-name"] != "" {
				record, e = store.Get("web", pos[1], user)
			} else {
				record, e = store.ResolveWeb(pos[1], user)
			}
		} else {
			if flags["exact-name"] != "" {
				record, e = store.Get("web", pos[1])
			} else {
				record, e = store.ResolveWeb(pos[1])
			}
		}
		if e != nil {
			return output(out, op, nil, e)
		}
		options := webfill.Options{TargetID: flags["target-id"], Origin: record.Origin, UsernameSelector: flags["username-selector"], PasswordSelector: flags["password-selector"], SubmitSelector: flags["submit-selector"], Username: record.Username, Password: record.Password}
		child, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var result webfill.Result
		if flags["cdp"] != "" {
			result, e = webfill.CDPFill(child, flags["cdp"], options)
		} else {
			result, e = webfill.Fill(child, dir, options)
		}
		return output(out, op, result, e)
	case "native-manifest":
		if len(pos) != 1 || unknown(flags, "extension-id", "binary") {
			return output(out, op, nil, model.Err("INVALID_ARGUMENT", "native-manifest参数无效"))
		}
		data, e := manifest(dir, flags["extension-id"], flags["binary"])
		if e != nil {
			return output(diag, op, nil, e)
		}
		fmt.Fprintln(out, string(data))
		return 0
	default:
		return output(out, op, nil, model.Err("INVALID_ARGUMENT", "未知命令"))
	}
}
func parse(args []string) ([]string, map[string]string, error) {
	pos := []string{}
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			key := strings.TrimPrefix(arg, "--")
			if _, exists := flags[key]; exists {
				return nil, nil, model.Err("INVALID_ARGUMENT", "参数重复")
			}
			if key == "exact-name" {
				flags[key] = "true"
				continue
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return nil, nil, model.Err("INVALID_ARGUMENT", "参数缺少值")
			}
			i++
			flags[key] = args[i]
		} else {
			pos = append(pos, arg)
		}
	}
	if len(pos) == 0 {
		return nil, nil, model.Err("INVALID_ARGUMENT", "缺少命令")
	}
	return pos, flags, nil
}
func unknown(flags map[string]string, allowed ...string) bool {
	for key := range flags {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
			}
		}
		if !found {
			return true
		}
	}
	return false
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func manifest(dir, id, binary string) ([]byte, error) {
	if len(id) != 32 {
		return nil, model.Err("INVALID_ARGUMENT", "需要32字符扩展ID")
	}
	for _, r := range id {
		if r < 'a' || r > 'p' {
			return nil, model.Err("INVALID_ARGUMENT", "扩展ID无效")
		}
	}
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, model.Err("INVALID_ARGUMENT", "配置目录无效")
	}
	if binary == "" {
		binary, e = os.Executable()
	}
	if e != nil || !filepath.IsAbs(binary) {
		return nil, model.Err("INVALID_ARGUMENT", "binary需要绝对路径")
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, model.Err("CONFIG_IO", "无法创建配置目录")
	}
	info, e := os.Lstat(abs)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, model.Err("UNSAFE_PERMISSIONS", "配置目录必须为私有目录")
	}
	script := filepath.Join(abs, "xweb-native-host.sh")
	content := "#!/bin/sh\nexec " + quote(binary) + " --data-dir " + quote(abs) + " native-host \"$@\"\n"
	if e = os.WriteFile(script, []byte(content), 0700); e != nil {
		return nil, model.Err("CONFIG_IO", "无法写native-host启动脚本")
	}
	if e = os.Chmod(script, 0700); e != nil {
		return nil, model.Err("CONFIG_IO", "无法保护native-host启动脚本")
	}
	return json.MarshalIndent(map[string]any{"name": "com.x_gateway.accessctl", "description": "xweb explicit-field browser bridge", "path": script, "type": "stdio", "allowed_origins": []string{"chrome-extension://" + id + "/"}}, "", "  ")
}
