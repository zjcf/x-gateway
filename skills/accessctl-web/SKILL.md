---
name: accessctl-web
description: 使用 xweb 受控保存网站凭据，按IP、URL或名称和当次CSS selector在日常Chrome或已有CDP页面填写，默认不提交，支持单字段或显式点击提交。
---

## 默认工作方式

用 `xweb`，工具仅保存网站名称、URL与凭据，不保存登录流程；Agent每次观察当前页面并给出目标和selector，用户无需编辑JSON。至少指定用户名或密码selector之一，允许多步页面分次填写。

帮助按日常命令、安装与维护命令分组，每条独立中文说明。三个顶层入口等效：

- `xweb -h`：显示帮助，短选项入口。
- `xweb --help`：显示帮助，长选项入口。
- `xweb help`：显示帮助，命令入口。

日常fill可直接使用用户给的IP或URL，不要求用户记NAME；帮助用fill SITE表示这个位置。`xweb credential list`每行仅有name/origin/username，add/delete成功result也是这三个字段，不读重复target或内部id；外层ok/operation保留。用 `xweb targets` 明确用户的profile/tab；多个同origin页面不可默认选第一行或当前活动页。选定单origin或名称后，省略--user时唯一账号自动使用；多账号报CREDENTIAL_AMBIGUOUS并列出排序去重username，不进行填写或提交，按用户目标明确--user USER。用户名大小写保留，空值或不存在不回退。新网站或新账号才保存首次自愿提供的username/password；不要搜Chrome密码库、其他秘密或其他聊天。首次主动提供经过Agent上下文，不额外回显；后续从工具内取值，不取密、猜测或重复索取。

例如list的一行结果是 `{"name":"monitoring","origin":"https://monitor.example.com","username":"operator"}`，不含认证秘密。

显式HTTP(S)URL和合法IP（含端口/IPv6）优先按地址查，即使有同名NAME；普通文本/域名先精确匹配NAME，未命中再按host查。URL忽略路径/查询/片段，只用规范origin；IP及数字端口规范化，HTTP默认80、HTTPS默认443，IPv6内嵌端口用[IPv6]:PORT。裸host多个origin时先按实际页面给完整URL，不用用户名猜协议。单origin同username跨NAME重复时，错误列出排序NAME，须明确选择，不任意挑记录。仅fill支持--exact-name强制精确NAME，必要时用它选择IP/URL样式名称；此选项不放宽页面origin校验。

## 日常命令：首次保存

```sh
xweb credential add NAME --url https://example.com/login --stdin  # 从受控stdin保存或更新网站账号凭据
```

PTY模式等待诊断流ready：`{"operation":"credential.add","ready":true,"input":"hidden_json_line"}`，工具关闭回显后才经该进程stdin发送一行JSON和换行。JSON严格仅username/password且非空，最多1MiB。使用序列化处理引号及非ASCII，不拼秘密到shell、argv、环境变量、明文临时文件或日志。管道可传完整JSON，普通文件重定向拒绝。没有可靠stdin时，用户在自己的终端运行不带--stdin的同一命令隐藏录入。

add按NAME+隐藏输入的username保存；同账号完整替换，不同账号互不覆盖，不给add传--user代替输入。同NAME各账号须同origin，不一致报CREDENTIAL_ORIGIN_CONFLICT，不放宽绑定；不同网站应使用不同名称。更新时重新提供用户名与密码，不从库内读回补齐。`--data-dir ROOT`在各命令中保持一致，实际文件ROOT/simple，自动管理本机key。共享库加密不抵御同OS用户取key解密，用户接受此边界。

## 日常命令：页面选择和填写

Chrome扩展当前v0.3.0，从旧版本升级后需手工重新加载原扩展一次，目录、ID和站点权限不变。Chrome按需启动xweb native-host，无独立常驻服务。首次安装用仓库根目录./install.sh；进入Chrome分支时用户按提示加载扩展并提供ID。Linux当前会话无DISPLAY/WAYLAND_DISPLAY时自动headless，只装工具、通用技能及PATH，可用已有CDP，无需Chrome或ID；这不代表机器没有桌面，显式--extension-id可强制远程预配置。具体步骤见README；不要因targets为空启动额外后台进程或每次登录重装。

```sh
xweb targets  # 查看日常Chrome可用标签页
xweb fill 10.0.0.20 --target-id TARGET_ID --password-selector '#password'  # 按IP选择凭据并填写密码
xweb fill https://example.com/login --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
xweb fill NAME --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 多账号时明确选择：
xweb fill NAME --user USER --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 重复别名或名称像IP/URL时，按已选元数据精确指定：
xweb fill NAME --exact-name --user USER --target-id TARGET_ID --password-selector '#password'
```

使用本次targets返回的完整opaque target_id，不把Chrome tab数字或Codex标签ID当作它。这个浏览器页面ID仍保留，与凭据中去掉的重复target不同。连接变化后重新查询。Agent观察表单时选择能明确定位目标的CSS；工具采用querySelector首匹配，不做unique保证。

只填密码页：

```sh
xweb fill NAME --target-id TARGET_ID --password-selector '#password'
```

只有用户任务要求提交，且页面按钮已明确时追加 `--submit-selector '#login'`。默认只填，不点击、不清空、不遮罩；填写后工具保留字段供用户或页面使用。字段派发input/change事件，提交最多点击一次，不自动重试认证。

已有CDP浏览器：

```sh
xweb targets --cdp http://127.0.0.1:9222  # 查看已有CDP浏览器的可用页面
xweb fill NAME --cdp http://127.0.0.1:9222 --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
```

endpoint仅loopback HTTP/WebSocket。CDP省略target-id只在唯一一个与凭据origin匹配的page时允许；歧义时明确目标。xweb不启动或关闭浏览器。

## 结果与边界

凭据登记URL的精确scheme/host/port限定注入目标，填值与点击还验证同URL/document。错误时处理明确的非秘密原因，不为跨域登录降级origin检查。只支持顶层普通input/textarea，不承诺iframe、Shadow DOM、SSO弹窗或MFA自动化。

结果的filled仅表示填值完成，submitted仅表示点击指定按钮，不证明登录成功。Agent随后观察并处理后续页面步骤，确认实际登录结果；验证码遵循实际浏览器工具当次操作要求，MFA/OTP确需用户输入时再请求，不承诺绕过验证。失败不反复提交。错误或取消可能留下部分已填值，也不能撤回已提交请求，不宣称字段已清理。

用户接受页面脚本及有页面权限的Agent能读字段，不要求清空、遮罩或旧版安全交接步骤；仍不要为无关目的读取/打印密码。fill结果仅target_id/filled/submitted，不含注入值。保持原浏览器与页面继续业务操作。

## 日常命令：查询与删除

- `xweb credential list`：查看网站名称、地址和用户名，结果仅name/origin/username。
- `xweb credential delete NAME [--user USER]`：删除明确账号或唯一记录，歧义不删除，不撤销已有Cookie。

add/delete仍精确使用NAME，不按IP/URL展开；多账号删除须明确--user。

## 安装与维护命令

- `./install.sh`：需Go1.25+，安装xssh/xweb及`~/.agents/skills/`内两份技能，按当前会话选择Chrome或headless；Chrome仅扩展模式需要。无GUI Linux不复制扩展、不生成shim/manifest、不询问ID，无TTY也可完成；显式`./install.sh --extension-id ID`强制Chrome配置。无凭据初始化，具体步骤见README。
- `./uninstall.sh`：按固定路径移除日常工具、`~/.agents/skills/`内两份技能、扩展文件与Native Messaging配置，无GUI也清理Chrome残留，默认保留全部凭据及其他用户数据；无需Go，不改Chrome profile或终止会话。用户须手动移除已加载的扩展条目，仅在用户要求卸载时使用，具体范围见README。
- `xweb credential rotate`：轮换共享库数据key，重加密全部SSH和Web凭据，不修改账号密码，machine.key不变；日常填写无需执行。
- `xweb native-host`：供Chrome自动调用，无需日常手动运行。
- `xweb native-manifest --extension-id ID [--binary ABS_PATH]`：生成Chrome扩展连接配置，不直接安装；配置保存位置及安装步骤见仓库README。

已有simple库的单账号记录自动兼容，读取不改盘，下次写入/轮换原子升级。Native Messaging host com.x_gateway.accessctl和技能目录accessctl-ssh/web是稳定安装标识，不重命名；工具入口只有xssh/xweb。
