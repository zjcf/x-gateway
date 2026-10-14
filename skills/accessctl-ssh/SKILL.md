---
name: accessctl-ssh
description: 使用 xssh 受控保存首次SSH凭据，随后按主机地址或user@host直接打开标准交互终端，适用于服务器和网络设备的连续CLI操作。
---

## 使用入口

默认用 `xssh [user@]host [-p PORT]`，直接使用本地凭据，无需常驻服务或额外登录配置。只给主机/IP时唯一账号可直接复用；多账号错误会列出排序去重用户名，按用户目标明确user@host后再连接，不任意挑第一项。已给用户却不存在时不回退其他用户。需要真实PTY；一次xssh进程就是一次标准SSH连接，连续命令通过同一进程stdin执行。退出后凭据与主机key仍保存，下次直接连接，不要求用户再次给密码。

帮助按日常命令、安装与维护命令分组，每条独立中文说明。三个顶层入口等效：

- `xssh -h`：显示帮助，短选项入口。
- `xssh --help`：显示帮助，长选项入口。
- `xssh help`：显示帮助，命令入口。

先用 `xssh credential list` 查账号、host和可选port元数据；port省略表示默认22。账户/主机/有效端口共同选取记录；连接明确端口时先筛该端口的账号，唯一则直接使用。选定账号仍有多个端口时错误列出排序端口，明确-p后再操作，不能默认选22；只有一个端口时可复用已存非22端口。裸IPv6按完整地址，不猜尾段端口，内嵌端口用[IPv6]:PORT。add/delete继续要求user@host，不能用裸主机。没有记录时只保存用户本次主动提供的凭据或其明确指定的私钥文件，不扫描其他秘密。首次主动提供已经过Agent上下文，不额外回显；后续无read/export取密入口。

## 日常命令：首次保存

```sh
xssh credential add user@host --stdin  # 从受控stdin保存或更新SSH凭据
# 非默认端口：追加 -p 2222
```

add省略端口使用22，不借用已有非默认端口；显式-p或地址内嵌端口会保存该值。默认22与显式22同身份。地址内嵌端口和-p同时给出时必须一致，显式-p22也不能与其他内嵌端口冲突。

以PTY启动进程，等诊断流输出关闭回显后的 `{"operation":"credential.add","ready":true,"input":"hidden_json_line"}`，再通过该进程stdin写一行序列化JSON和换行。不要在ready前输入，不把秘密拼到shell、argv、环境变量或明文临时文件。没有受控stdin时让用户在自己的终端运行不带--stdin的同一命令，隐藏录入密码。

SSH JSON允许username/password/private_key/passphrase/enable_password/sudo_password；username可省略，提供时须与目标账户一致；密码或有效私钥至少一个。私钥用JSON转义编码，加密私钥须正确passphrase。当前xssh不使用ssh-agent；enable/sudo字段可保存，但标准终端不会自动识别提示并注入。整份输入最多1MiB，私钥最多128KiB。add完整替换，遗漏字段会清空，更新时须重新提供需要保留的认证值，不从库内读回补齐。

存储自动创建，无主口令。`--data-dir ROOT`选择统一ROOT，实际文件位于ROOT/simple；不要为一次错误另建空库。文件加密不抵御同OS用户取key解密，这是用户接受的边界。

## 日常命令：连接与连续操作

```sh
xssh host  # 使用该主机唯一匹配的已存账号登录
xssh user@host  # 使用明确账号登录
xssh user@host -p 2222  # 使用明确账号和端口登录
xssh 2001:db8::10  # 使用裸IPv6主机登录
xssh '[2001:db8::10]:2222'  # 使用IPv6及内嵌端口登录
xssh 'user@[2001:db8::10]' -p 2222  # 使用IPv6、明确账号和端口登录
```

用工具的真实PTY会话保留连接；命令和控制字符直接写stdin。支持远端PTY、ANSI/UTF-8、Ctrl-C、窗口变化及真实退出码。raw输出不脱敏，终端可能显示身份或远端敏感内容；不要主动发送认证值来测试回显。

第一次连接自动TOFU固定主机key并显示指纹，无需额外登记命令或一律要求用户人工登记。TOFU不等于独立身份验证。`HOST_KEY_CHANGED`必须停止并独立核验，不自动删pin、换库、改端口或重信任。认证失败不猜密码或自动重试其他认证值；断开不重放命令。

初次连接后只做适合目标的最小只读确认，例如已知Linux的whoami/pwd，或已知网络设备的版本/状态查看；不为验证进配置模式、保存配置或提升权限。后续操作按用户任务授权。安静窗口或提示符不代表业务成功；远端exit-status才是进程退出依据，缺失状态的断连返回255。

## 日常命令：查询与删除

- `xssh credential list`：查看账号、host和port元数据，不导出认证秘密。
- `xssh credential delete user@host [-p PORT]`：按唯一或显式端口规则删除对应凭据，不关闭既有连接、不删除pin。

## 安装与维护命令

- `./install.sh`：需Go1.25+，在项目根目录安装xssh/xweb、通用`~/.agents/skills/`内两份技能及PATH。macOS默认Chrome；Linux当前会话无DISPLAY/WAYLAND_DISPLAY时自动headless，无需Chrome、TTY或ID，仍可SSH及已有CDP接入。有GUI时用户按提示加载扩展并提供ID；显式`./install.sh --extension-id ID`会强制配置Chrome，可远程预配置。按会话判断不等于机器无桌面，步骤见README；不要每次登录重装。
- `./uninstall.sh`：按固定路径移除日常工具、`~/.agents/skills/`内两份技能与浏览器连接文件，无GUI也清理Chrome残留；保留全部凭据、pins及其他用户数据，无需Go，不终止现有SSH会话。Chrome扩展条目由用户手动移除，仅在用户要求卸载时使用，具体范围见README。
- `xssh credential rotate`：轮换共享库数据key，重加密全部SSH和Web凭据，不修改账号密码，machine.key不变；日常登录无需执行。

已有xssh/xweb的simple库自动兼容，读取不改盘，下次写入/轮换原子升级；旧SSH非默认端口会恢复，直接无-p连接可复用唯一记录。技能目录accessctl-ssh/web是稳定安装标识，保留名称；工具入口只有xssh/xweb。
