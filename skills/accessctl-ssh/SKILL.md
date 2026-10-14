---
name: accessctl-ssh
description: 当用户需要通过 SSH 登录服务器或网络设备、执行远程命令，或管理 SSH 登录凭据时使用。通过 xssh 复用已保存账号并打开交互终端。
---

## 工具依赖

使用前须安装外部 x-gateway 工具 `xssh` 并加入 PATH；安装技能不会安装工具。安装、升级及卸载说明见 [x-gateway README](https://github.com/zjcf/x-gateway/blob/main/README.md)。

## 登录与远程操作

默认按用户给出的 IP 或主机名连接，复用已有凭据；无需每次先查询凭据：

```sh
xssh host
xssh user@host
xssh user@host -p 2222
```

需要确认已保存的账号或端口时用 `xssh credential list`，只读取元数据。选择规则：

- 唯一账号与端口可直接复用，包括已保存的非 22 端口。多账号须明确 `user@host`，同账号多端口须明确 `-p PORT`；不任意选第一条，也不默认选 22。
- 用户明确端口时先按端口筛选账号；已明确账号或端口却无匹配记录时，不回退其他记录。
- 裸 IPv6 作为完整主机地址；内嵌端口用 `[IPv6]:PORT`，账号形式为 `user@[IPv6]:PORT`。内嵌端口与 `-p` 同时给出时必须一致。

以工具的真实 PTY 会话启动 `xssh`，通过同一进程 stdin 发送连续命令及 Ctrl-C 等控制字符。每次进程都是独立 SSH 连接，退出后不恢复远端 shell 状态或重放命令。输出为原始终端内容，可能含敏感信息，不发送认证值测试回显。

首次连接会自动固定主机 key 并显示指纹（TOFU），这不构成独立身份核验。遇到 `HOST_KEY_CHANGED` 必须停止并独立核验；不删 pin、换数据目录、改端口或自动重新信任来绕过。

连接后按已知系统执行最小只读确认，例如 Linux 的 `whoami` / `pwd`、网络设备的版本或状态命令；后续操作限于用户任务范围。认证失败不猜密码、不自动尝试其他认证值，断连不重放命令。

提示符或安静输出不代表任务成功，按远端实际输出验证结果。进程正常退出返回远端 exit-status；断连且缺失退出状态时返回 255。

## 首次保存或更新凭据

没有匹配记录时，仅使用用户本次主动提供的凭据或其明确指定的私钥文件，不扫描其他秘密：

```sh
xssh credential add user@host --stdin
# 非默认端口追加 -p 2222
```

以真实 PTY 启动，等待诊断流输出以下 ready 消息，工具关闭回显后才通过该进程 stdin 写入一行序列化 JSON 和换行：

```json
{"operation":"credential.add","ready":true,"input":"hidden_json_line"}
```

不要提前输入，不把秘密拼进 shell、argv、环境变量、日志或明文临时文件。没有可靠的受控 stdin 时，让用户在自己的终端运行不带 `--stdin` 的同一命令，隐藏录入。

SSH JSON 允许 `username`、`password`、`private_key`、`passphrase`、`enable_password`、`sudo_password`。`username` 可省略，提供时须与目标账号一致；密码或有效私钥至少一个。私钥须用 JSON 转义编码，加密私钥需要正确 passphrase；输入最多 1 MiB，私钥最多 128 KiB。

`add` 完整替换该账号和端口的记录，遗漏字段会清空；更新时重新提供需要保留的认证值，不从库内读回补齐。省略端口登记 22，不借用已存非默认端口；显式 `-p` 与内嵌端口须一致。当前不使用 ssh-agent，也不自动识别或注入 enable/sudo 密码。

使用 `--data-dir ROOT` 时，各次操作保持同一目录，实际凭据位于 `ROOT/simple`。文件加密不能抵御同 OS 用户读取本机 key 后解密；CLI 不提供认证值的读取或导出入口。

## 查询、删除与轮换

```sh
xssh credential list
xssh credential delete user@host [-p PORT]
xssh credential rotate
```

删除须明确 `user@host`；省略端口仅在该账号与主机下唯一记录时允许，多端口须明确 `-p`。删除不关闭既有连接，也不删除主机 key pin。

`rotate` 轮换共享库数据 key，重加密全部 SSH 和 Web 凭据，不修改业务账号密码，`machine.key` 不变；日常登录无需执行。
