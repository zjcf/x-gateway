# 本地假 SSH 验收

此服务仅用于本地测试，不启动 daemon，也不创建凭据库。每次启动生成临时主机 key；同一进程可供多个 xssh 进程顺序登录。

```sh
go run ./scripts/simple-ssh-fixture --listen 127.0.0.1:22222
xssh --data-dir /tmp/xssh-isolated credential add demo@127.0.0.1 -p 22222
xssh --data-dir /tmp/xssh-isolated demo@127.0.0.1 -p 22222
```

公开假账户 `demo`，假密码 `fake-xssh-password`。命令：`whoami`、`show status`、`show size`、`ansi`、`exit`、`exit 7`、`disconnect`。Ctrl-C清当前输入并返回提示，Ctrl-D退出；`ansi`返回原始ANSI与UTF-8，`show size`观察PTY尺寸。

停止后重启服务会换 key，已有 xssh pin 应拒绝连接；请使用新的隔离数据目录开始下一轮，不能把该做法用于绕过真实主机变更核验。
