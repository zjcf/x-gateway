package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"x-gateway/internal/credentialcli"
	"x-gateway/internal/localstore"
	"x-gateway/internal/model"
	"x-gateway/internal/terminal"
)

const help = `用法：
  xssh [全局选项] 命令 [参数]

日常命令：
  xssh [user@]HOST [-p PORT]  # 使用已保存凭据登录SSH终端
  xssh credential add user@HOST [--stdin] [-p PORT]  # 保存或更新指定账号的SSH凭据
  xssh credential list  # 查看已保存的SSH账号、主机和端口
  xssh credential delete user@HOST [-p PORT]  # 删除指定账号和端口的SSH凭据
  xssh -h  # 显示帮助
  xssh --help  # 显示帮助
  xssh help  # 显示帮助

安装与维护命令：
  xssh credential rotate  # 轮换数据加密密钥，重新加密当前库全部SSH和Web凭据，不改账号密码

全局选项：
  --data-dir DIR  指定数据根目录

说明：
  登录可省略用户，唯一匹配时使用已保存账号和端口；多账号须指定user@HOST，多端口须指定-p。
  HOST支持主机名、IPv4和IPv6；IPv6内嵌端口写作[IPv6]:PORT。
  新增凭据不指定端口时默认22；新增、删除必须指定user@HOST。
  首次自动固定主机密钥，变化时拒绝连接；凭据仅通过隐藏输入或--stdin保存。
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	dir := localstore.DefaultDir()
	remaining := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--data-dir" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "INVALID_ARGUMENT: --data-dir需要目录")
				return 2
			}
			i++
			dir = args[i]
		} else if strings.HasPrefix(arg, "--data-dir=") {
			dir = strings.TrimPrefix(arg, "--data-dir=")
		} else {
			remaining = append(remaining, arg)
		}
	}
	if len(remaining) == 0 || remaining[0] == "--help" || remaining[0] == "-h" || remaining[0] == "help" {
		fmt.Fprint(os.Stdout, help)
		return 0
	}
	if remaining[0] == "credential" {
		return credentialcli.Run(ctx, "ssh", dir, remaining[1:], os.Stdin, os.Stdout, os.Stderr)
	}
	target, port, err := parseTarget(remaining)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_, err = localstore.ParseSSHConnection(target, port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "INVALID_ARGUMENT: SSH目标无效")
		return 2
	}
	store, err := localstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	record, err := store.GetSSH(target, port)
	if err != nil {
		var coded *model.Error
		if errors.As(err, &coded) && coded.Code == "CREDENTIAL_NOT_FOUND" {
			fmt.Fprintln(os.Stderr, "CREDENTIAL_NOT_FOUND: 请先使用 xssh credential add 保存该目标凭据")
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	canonical := record.Target
	at := strings.LastIndex(canonical, "@")
	host, portString, err := net.SplitHostPort(canonical[at+1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "INVALID_ARGUMENT: SSH目标无效")
		return 2
	}
	port, _ = strconv.Atoi(portString)
	code, err := terminal.Run(ctx, terminal.Config{Host: host, Port: port, Username: record.Username, Password: record.Password, PrivateKey: record.PrivateKey, Passphrase: record.Passphrase, DataDir: dir, Term: os.Getenv("TERM")}, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return code
}

func parseTarget(args []string) (string, int, error) {
	target := ""
	port := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-p" || arg == "--port" {
			if i+1 >= len(args) {
				return "", 0, fmt.Errorf("INVALID_ARGUMENT: -p需要端口")
			}
			i++
			n, e := strconv.Atoi(args[i])
			if e != nil || n < 1 || n > 65535 || port != 0 {
				return "", 0, fmt.Errorf("INVALID_ARGUMENT: SSH端口无效")
			}
			port = n
		} else if strings.HasPrefix(arg, "-") || target != "" {
			return "", 0, fmt.Errorf("INVALID_ARGUMENT: 用法 xssh [user@]HOST [-p PORT]")
		} else {
			target = arg
		}
	}
	if target == "" {
		return "", 0, fmt.Errorf("INVALID_ARGUMENT: 缺少HOST")
	}
	return target, port, nil
}
