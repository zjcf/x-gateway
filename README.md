# x-gateway

`xssh` 和 `xweb` 是两个直接使用本地加密凭据的 Go 工具。录入一次，以后 SSH 按主机地址连接，必要时明确 `user@host`，网页按 IP、URL 或网站名称和当次页面 selector 填写。工具直接运行，无常驻后台服务或流程 JSON 配置。

- `xssh`：标准交互 SSH 终端，支持 raw TTY、PTY、Ctrl-C、窗口大小变化和远端退出码。
- `xweb`：把已保存的用户名或密码填到指定浏览器页面，默认不提交；可明确指定提交按钮。

## 一键安装

共同前置：macOS 或 Linux、Go 1.25+。需要 Chrome 扩展接入时才需要 Google Chrome。在仓库根目录运行：

```sh
./install.sh  # 安装工具和通用技能，按当前会话选择是否配置Chrome
```

技能统一安装到 `~/.agents/skills/`。macOS 默认配置 Chrome；Linux 当前会话中 `DISPLAY` 或 `WAYLAND_DISPLAY` 任一非空时配置 Chrome，否则自动采用 headless 安装：只安装 xssh/xweb、两份技能及 PATH，不复制扩展、不生成 shim/manifest、不询问 ID，无 TTY 也可完成，xweb 仍可连接已有 CDP。headless 不删除此前的 Chrome 文件。

这是按**当前会话**判断；例如 SSH 会话没有 DISPLAY 时会走 headless，并不表示这台机器没有桌面或未安装 Chrome。

需要 Chrome 扩展模式时，脚本先安装文件，首次只需完成两个人工步骤：

1. 按脚本提示打开 `chrome://extensions`，开启开发者模式，“加载已解压的扩展程序”，选择 `~/.local/share/x-gateway/extension`，接受扩展权限。
2. 复制该扩展的 32 字符 ID，回到脚本提示中粘贴。脚本随后生成并保存 Native Messaging 配置。

默认安装位置：

| 内容 | 路径 |
| --- | --- |
| 日常工具 | `~/.local/bin/xssh`、`~/.local/bin/xweb` |
| 通用 Agent 技能 | `~/.agents/skills/accessctl-ssh`、`~/.agents/skills/accessctl-web` |
| Chrome 扩展（仅 Chrome 模式） | `~/.local/share/x-gateway/extension`，升级时保持此路径与扩展 ID |
| macOS Native Messaging manifest（仅 Chrome 模式） | `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.x_gateway.accessctl.json` |
| Linux Native Messaging manifest（仅 Chrome 模式） | `${XDG_CONFIG_HOME:-$HOME/.config}/google-chrome/NativeMessagingHosts/com.x_gateway.accessctl.json` |

脚本为 zsh/bash 自动补充缺失的 PATH 配置：zsh 写 `${ZDOTDIR:-$HOME}/.zshrc`，bash 在 macOS 写 `~/.bash_profile`、Linux 写 `~/.bashrc`，重复运行不重复添加。新终端生效；当前终端可先使用 `~/.local/bin/xssh`、`~/.local/bin/xweb`。其他 shell 按脚本提示配置。安装无需凭据初始化，首次保存凭据仍使用下文的隐藏输入。

已有扩展 ID 时可重复安装或升级，无需再次等待输入。显式 `--extension-id ID` 会强制配置 Chrome，即使当前 Linux 会话没有 GUI，也可用于 SSH 远程预配置：

```sh
./install.sh --extension-id EXTENSION_ID  # 使用已有ID重新构建并升级工具、技能与连接配置
./install.sh -h  # 查看安装脚本帮助
./install.sh --help  # 查看相同帮助
```

保留原扩展目录和 ID。仅更新工具、技能或连接配置，扩展内容未变化时无需重新加载；升级包含扩展文件变化时，在 `chrome://extensions` 对原扩展点击“重新加载”，Chrome 提示新增权限时再确认，不另建新 ID。脚本不代替浏览器的加载扩展操作。

## 一键卸载

在仓库根目录、以安装时的同一用户与环境运行，无需 Go：

```sh
./uninstall.sh  # 移除日常工具、技能、扩展文件和连接配置，保留凭据
./uninstall.sh -h  # 查看卸载脚本帮助
./uninstall.sh --help  # 查看相同帮助
```

脚本仅删除上方安装位置的 xssh/xweb、`~/.agents/skills/` 中两份技能目录、扩展目录、当前平台 Chrome Native Messaging manifest，以及默认数据 ROOT 下的 `xweb-native-host.sh`；Linux 设置过 `XDG_CONFIG_HOME` 时须沿用相同环境。卸载不按 GUI 分支跳过 Chrome 残留：无 DISPLAY 的会话也按固定路径清理。重复运行时，已经不存在的项目视为成功；未知参数会在删除前拒绝，没有 purge 选项。目标叶子若是符号链接，只移除链接，不删除其指向的内容。

全部凭据默认保留：`ROOT/simple/` 内的密文、`machine.key` 和主机 key pins 均不删除。其他用户数据、公共 PATH 配置和安装父目录也保留；以后同路径重新安装可继续使用保存的凭据。

Chrome 中已加载的扩展条目仍需用户在 `chrome://extensions` 手动移除。脚本不修改浏览器 profile、不关闭浏览器或终止现有会话；native-host 在扩展移除或连接关闭后退出，正在使用的 SSH 会话由用户正常退出。

## 手动构建（高级）

需要 Go 1.25+，当前交互终端支持 macOS/Linux。Node.js 只用于扩展测试，Chrome/Chromium 只用于网页功能；运行二进制不需要 Node.js、Python 或 sshpass。

```sh
make build
./bin/xssh -h      # 显示xssh帮助（短选项）
./bin/xssh --help  # 显示xssh帮助（长选项）
./bin/xssh help    # 显示xssh帮助（命令入口）
./bin/xweb -h      # 显示xweb帮助（短选项）
./bin/xweb --help  # 显示xweb帮助（长选项）
./bin/xweb help    # 显示xweb帮助（命令入口）
```

生成 `bin/xssh`、`bin/xweb`。将这两个二进制放到 PATH 后可直接使用下文命令。

每个工具的三个顶层帮助入口输出相同内容。帮助按“日常命令”和“安装与维护命令”分组，每条完整命令独立一行并附中文用途，不用 `|` 合并；不要求给子命令额外加 help。

## 凭据保存与本地文件

两个工具共享同一份本地存储。默认 ROOT 是系统用户配置目录下的 `x-gateway`：macOS 通常为 `~/Library/Application Support/x-gateway`，Linux 通常为 `$XDG_CONFIG_HOME/x-gateway` 或 `~/.config/x-gateway`。实际文件位于 `ROOT/simple/`；`--data-dir ROOT` 替换 ROOT，工具仍会追加 `simple/`。

目录权限为 0700，`machine.key` 和 `credentials.json` 为 0600。工具自动生成本机随机 key，以内嵌 seed 作派生域，从本机 key 派生包装 key；随机数据 key 加密凭据，包装 key 加密数据 key。文件只保存密文，CLI 没有 read/export 明文入口。

这用于防止凭据意外明文落盘或由 CLI 返回，**不抵御同一 OS 用户读取 key 后解密**。内嵌 seed 不是秘密。备份应同时保留整个 `ROOT/simple/`；单独备份密文无法在丢失 `machine.key` 后恢复。已有密文而本机 key 缺失时，工具拒绝自动生成新 key，不覆盖旧数据。

用户可以在自己的终端隐藏录入，也可以首次主动把业务凭据提供给 Agent，让 Agent 通过受控 stdin 保存。首次主动提供的内容已经经过 Agent 和聊天上下文；后续工具从库内取值，无需重复索取。

### 日常命令：人工隐藏录入

```sh
xssh credential add admin@10.0.0.10  # 保存或更新默认端口的SSH凭据
xssh credential add admin@10.0.0.10 -p 2222  # 保存或更新指定端口的SSH凭据
xweb credential add monitoring --url https://monitor.example.com/login  # 保存或更新网站账号凭据
```

SSH 账户来自目标地址；人工录入密码。网站人工录入用户名和密码。网站按 `NAME + username` 独立保存，同一网站可有多个账号，用户名保留大小写。再次 add 同一账号会完整替换该记录，其他账号不变；请重新提供所有需要保留的字段。SSH 必须有密码或有效私钥；当前 `xssh` 不使用 ssh-agent。

SSH add 未指定端口时使用 22，指定 `-p PORT` 或地址内嵌端口时保存该端口。默认 22 与显式 22 是同一个目标，不会重复建记录。网站同一 NAME 下各账号须属于同一 origin，不一致时返回 `CREDENTIAL_ORIGIN_CONFLICT`，不覆盖已有记录。

### 日常命令：Agent 首次受控导入

```sh
xssh credential add admin@10.0.0.10 --stdin  # 从受控stdin保存SSH凭据
xweb credential add monitoring --url https://monitor.example.com/login --stdin  # 从受控stdin保存网站凭据
```

TTY 模式先关闭回显，向诊断流输出以下 ready 消息，再接受一行 JSON；Agent 必须等 ready 后才通过该进程的 stdin 写入。多行私钥用 JSON 转义编码，不写明文临时文件。

```json
{"operation":"credential.add","ready":true,"input":"hidden_json_line"}
```

SSH JSON 允许 `username`、`password`、`private_key`、`passphrase`、`enable_password`、`sudo_password`。`username` 可省略，提供时须与 `user@host` 一致；`password` 或有效 `private_key` 至少一个。网站 JSON 只允许 `username` 和 `password`，两者非空。私钥最多 128 KiB，整份 JSON 最多 1 MiB；未知字段和多余 JSON 内容被拒绝。管道模式读取完整 JSON，普通文件重定向被拒绝。

秘密不得放进 argv、shell 命令、环境变量、明文临时文件或日志；不要额外回显。仅使用用户本次主动提供的内容或明确指定的私钥文件，不扫描其他秘密。保存结果仅返回元数据。

### 日常命令：查询与删除

```sh
xssh credential list  # 查看已保存的SSH账号、主机和端口
xweb credential list  # 查看网站名称、地址和用户名
xssh credential delete admin@10.0.0.10 -p 2222  # 删除指定账号和端口的SSH凭据
xweb credential delete monitoring  # 删除该网站唯一账号的凭据
xweb credential delete monitoring --user operator  # 删除指定网站账号的凭据
```

网站 credential list 的每行、add 和 delete 的成功结果都只含 `name`、网站地址 `origin`、`username`，不重复输出 target，也不含 id、kind、saved/deleted 或密码；外层仍有 `ok` 和 `operation`。例如：

```json
{"ok":true,"operation":"credential.list","result":[{"name":"monitoring","origin":"https://monitor.example.com","username":"operator"}]}
```

add/delete 的 `result` 是同样三个字段的单个对象；delete 返回实际删除的账号元数据。网站 delete 省略 `--user` 时须只有一个账号，多账号返回 `CREDENTIAL_AMBIGUOUS`，列出已存用户名并提示 `--user`，不会删除；显式 `--user USER` 只删除该账号，空值被拒绝。

SSH 输出保持原有字段，包括账号、host 和可选 port；port 省略表示未显式指定、有效端口为 22。SSH add/delete 仍须给 `user@host`，不接受裸主机；delete 省略端口时，按账户和主机查找唯一记录并复用其已存端口，多端口报歧义，不默认挑 22。

删除凭据不会关闭已有 SSH 连接或撤销浏览器 Cookie，也不会删除主机 key pin。

### 安装与维护命令：共享库轮换

```sh
xssh credential rotate  # 轮换共享库数据key，重新加密全部SSH和Web凭据，不改账号密码
xweb credential rotate  # 执行同一共享库轮换，不改账号密码
```

两条命令等效，都原子重加密 **SSH 和网站全部记录**，不是只轮换调用工具的那一类；不修改服务器或网站的账号密码，`machine.key` 保持不变。日常登录无需轮换，轮换成功后原目标仍可直接使用。

## 日常命令：SSH 标准终端

```sh
xssh 10.0.0.10  # 使用该主机唯一匹配的已存账号登录
xssh admin@10.0.0.10  # 使用明确账号登录
xssh admin@10.0.0.10 -p 2222  # 使用明确账号和端口登录
xssh 2001:db8::10  # 使用裸IPv6主机登录
xssh '[2001:db8::10]:2222'  # 使用IPv6及内嵌端口登录
xssh 'admin@[2001:db8::10]' -p 2222  # 使用IPv6、明确账号和端口登录
```

连接可只给主机名或 IP：唯一账号且唯一端口时直接复用；多个账号返回 `CREDENTIAL_AMBIGUOUS`，列出排序去重的用户名，要求改用 `user@host`。如果明确端口，先按端口筛选账号，例如只有 admin 使用 2222 时，`xssh 10.0.0.10 -p 2222` 可直接选择该记录。已明确 `user@host` 却没有匹配账号时直接失败，不能回退到其他用户。

保存的目标规范化为 `user@host:port`，IPv6 使用括号。账户、主机和有效端口共同定位凭据。未给 `-p` 且地址没有内嵌端口时，会复用已选账号的唯一已存端口：例如只保存了 2222，直接连接就使用 2222；若同账号同时有 22 和 2222，则报歧义并列出排序端口，须明确 `-p`，不会默认选 22。裸 IPv6 作为完整主机地址解析，不把最后一段猜成端口；IPv6 内嵌端口须写 `[IPv6]:PORT`。

明确给 `-p PORT` 或地址内嵌端口时只匹配该端口；两种写法同时出现必须一致，包括显式 `-p 22` 与内嵌其他端口冲突也会拒绝。新增凭据与连接的省略规则不同：`credential add` 省略端口总是登记默认 22，不借用已有非默认端口。

首次连接自动固定观察到的主机 key，输出 SHA256 指纹和 TOFU 提示。pin 保存于 `ROOT/simple/known_hosts/`，按主机和端口共享，文件名是该地址的 SHA256。TOFU 不构成独立身份核验；后续 `HOST_KEY_CHANGED` 必须停止并独立核验，不能自动覆盖、删 pin 或换数据目录绕过。

SSH 使用 Go 实现密码/私钥认证，直接申请远端 PTY。终端输入输出保持原始字节，不做输出脱敏或命令包装；支持 ANSI、UTF-8、控制字符和 SIGWINCH。Ctrl-C 送到远端，远端正常退出返回实际退出码；连接中断且没有退出状态时返回 255。退出、失败或终止时恢复本地终端。需要真实 TTY；非交互管道会被拒绝。

每次 `xssh` 进程持有自己的连接，退出即关闭；后续进程复用保存的凭据和 pin，新建 SSH 连接，不恢复远端 shell 状态、不自动重放命令。当前没有 `-L` 监听或 Web 终端。

本地假服务的启动方式与命令见 [fixture 说明](scripts/simple-ssh-fixture/README.md)。不要用真实设备做自动测试。

## 网页按当次 selector 填写

`xweb` 只保存网站名称、登录 URL 和各账号凭据，不保存 selector 或登录流程。帮助中的 `fill SITE` 表示 SITE 可直接给 IP、URL 或名称，无需记名称；add/delete 仍使用 NAME 管理记录。Agent 每次观察当前页面，选定页面、账号与 CSS selector 后调用 fill；至少指定用户名或密码 selector 之一，可分别处理多步登录页。

### 按 IP、URL 或名称选凭据

```sh
# 已登记的URL为http://10.0.0.20/login时，可直接给IP：
xweb fill 10.0.0.20 --target-id TARGET_ID --password-selector '#password'
# URL只用origin查库，不要求与登记路径相同：
xweb fill https://monitor.example.com/dashboard --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 多账号时明确用户名：
xweb fill 10.0.0.20 --user operator --target-id TARGET_ID --password-selector '#password'
```

解析优先级：显式 HTTP(S) URL 和合法 IP（含端口或 IPv6）按地址查找，即使有同名 NAME；普通文本或域名先匹配精确 NAME，未命中才按 host 查找。URL 忽略路径、查询和片段，按规范 origin 匹配；IP 统一规范形式，端口按数值处理，默认 HTTP 80、HTTPS 443 归一。裸 host 不猜协议，`host:PORT` 按有效端口筛选；IPv6 可用裸地址，内嵌端口须写 `[IPv6]:PORT`。

裸主机若匹配多个 origin，返回 `CREDENTIAL_AMBIGUOUS` 并要求完整 HTTP(S) URL，不根据用户名猜 scheme/port。选定单一 origin 后，唯一账号自动使用；多个账号列出排序去重用户名，要求 `--user USER`。同 origin、同 username 若在不同 NAME 下重复保存，则列出排序名称，要求明确记录，不任意挑选。

必要时用仅供 fill 的 `--exact-name` 强制按 NAME 查库，尤其适用于名称恰好像 IP/URL 或重复别名需要精确选择时：

```sh
xweb fill 10.0.0.20 --exact-name --user operator --target-id TARGET_ID \
  --password-selector '#password'
```

此选项只改变凭据查找方式，不放宽 origin；即使按 IP 查库或精确名称选记录，实际注入仍受记录的完整 origin 限制。

### 安装与维护命令：Chrome 接入

使用上方 `./install.sh` 已完成扩展文件和 Native Messaging 配置安装时，可直接进入下一节。以下为高级手动安装路径。Chrome 按需启动 `xweb native-host`。扩展当前为 v0.3.0；从旧版本升级后须在原扩展条目手动“重新加载”一次，目录、ID 和已有站点权限保持。

- `xweb native-host`：供 Chrome 自动调用，无需日常手动运行。
- `xweb native-manifest --extension-id ID --binary /absolute/path/xweb`：生成 Chrome 扩展连接配置，不直接安装 manifest 或扩展。

1. 首次安装时，将仓库 `extension/` 放到稳定路径，例如 `~/.local/share/x-gateway/extension`。在日常 Chrome 的 `chrome://extensions` 开启开发者模式，加载该目录，接受 HTTP/HTTPS 站点权限，记下扩展 ID。已有安装保留原路径和 ID。
2. 用已安装 xweb 的绝对路径生成并安装 Native Messaging manifest。macOS 示例：

```sh
mkdir -p "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
xweb native-manifest --extension-id EXTENSION_ID --binary /absolute/path/xweb \
  > "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.x_gateway.accessctl.json"
```

Linux Google Chrome 的 manifest 目录为 `${XDG_CONFIG_HOME:-$HOME/.config}/google-chrome/NativeMessagingHosts/`。命令 stdout 是 manifest JSON，生成的 `ROOT/xweb-native-host.sh` 为 0700；host 名 `com.x_gateway.accessctl` 和技能目录 `accessctl-ssh`、`accessctl-web` 是已安装的稳定标识，保留名称不表示另有产品命令。manifest 只允许指定扩展 ID。Chrome 按需启动 shim，扩展断开时 native-host 退出；更新连接配置后可通过扩展的“重新连接”接入新 shim。

### 日常命令：选择 Chrome 页面并填写

打开目标页面后查询标签页，再明确填写：

```sh
xweb targets  # 查看日常Chrome可用标签页
xweb fill monitoring --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 同名网站有多个账号时，明确选择：
xweb fill monitoring --user operator --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 只有明确需要提交时才增加：--submit-selector 'button[type=submit]'
```

TARGET_ID 必须使用当次 targets 返回的完整 opaque 值，包含具体连接和标签页身份；不直接使用 Chrome tab 数字。浏览器 `target_id` 用于选择页面，与已移除的凭据重复字段 target 不同，仍需保留。targets 只列 origin 和身份元数据，不返回路径、标题或认证值。连接更新后重新查询，不猜旧 ID。

fill 省略 `--user` 时，已选名称或单一 origin 下只有一个账号就自动使用；多个账号返回 `CREDENTIAL_AMBIGUOUS`，列出排序去重的已存用户名并提示 `--user USER`，不填写、不提交。`--user USER` 精确匹配用户名，保留大小写；不存在或空值会失败，不回退到其他账号。同用户跨名称重复的情况仍须明确 NAME。add 的用户名仍来自隐藏输入或 stdin JSON，不接受 `--user` 代替录入；delete 不按 IP/URL 扩大查找范围，继续精确 NAME。

### 已有 CDP 浏览器

```sh
xweb targets --cdp http://127.0.0.1:9222  # 查看已有CDP浏览器的可用页面
xweb fill monitoring --cdp http://127.0.0.1:9222 --target-id TARGET_ID \
  --password-selector '#password' --submit-selector '#login'
```

CDP endpoint 只接受 loopback HTTP 或 WebSocket。省略 `--target-id` 时，必须恰好有一个与已登记 origin 匹配的页面；多个匹配页面会返回 `TARGET_AMBIGUOUS`。工具只连接现有浏览器，不启动或关闭它。

### 页面行为与结果

凭据只向登记 URL 的精确 scheme/host/port 注入，路径不是绑定边界；HTTP 和 HTTPS 是不同 origin。填写与提交均核验同一 document 和当前 URL，页面变化会失败。selector 使用 `querySelector` 的首匹配语义，Agent 应选能明确定位目标的 CSS，不承诺唯一匹配校验。

支持顶层普通 input/textarea，派发 input/change 事件。默认只填，不提交、不清空、不遮罩字段；网站脚本及拥有页面权限的客户端仍可读到字段值，这是用户接受的模式。可选提交最多点击一次，不自动重试认证，也不保证任意 iframe、Shadow DOM、SSO 弹窗或 MFA。

成功结果仅为：

```json
{"target_id":"...","filled":true,"submitted":false}
```

`filled` 表示填值操作完成，`submitted` 表示点击过指定按钮，**都不证明登录成功**。Agent 随后观察并处理后续页面步骤，确认实际登录结果；验证码遵循实际浏览器工具当次操作要求，MFA/OTP 确需用户输入时再请求，不承诺绕过验证。错误或取消可能留下已填字段，工具不自动清理或撤销已发出的请求。返回不含凭据值，已登录的浏览器页面仍由用户持有。

## simple 数据格式兼容

已有 xssh/xweb 的 `ROOT/simple/` 密文记录自动兼容，包括原有单账号网站与 SSH 记录，不要求人工迁移。旧网站记录按已有 username 映射到多账号身份；读取不改盘，下次成功写入或轮换时整库原子升级。旧 SSH 非 22 端口从目标恢复，旧 `:22` 按默认端口处理。

## 验证与代码入口

```sh
make test
make vet
```

核心路径：`cmd/xssh` / `internal/terminal`、`cmd/xweb` / `internal/webfill`、`internal/credentialcli` / `internal/localstore`。测试覆盖当前工具、simple 数据兼容、安装卸载与浏览器扩展；验收 fixture 仅使用隔离假凭据。

Agent 技能入口：[SSH](skills/accessctl-ssh/SKILL.md)、[网页](skills/accessctl-web/SKILL.md)。架构与边界见 [设计说明](docs/architecture/agent-access-tool-design.md)。
