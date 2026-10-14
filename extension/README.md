# 日常 Chrome 的 xweb 接入

本目录提供原生 JavaScript Manifest V3 扩展 v0.3.0。它在安装时取得 HTTP/HTTPS 站点权限，按 profile 自动连接 Native Messaging，并自动同步标签页库存。xweb 使用 current/visible/fill/click 协议及取消控制，无需逐站授权或为每个 tab 点击 popup；升级到本版本后在 Chrome 管理页手动重新加载原扩展一次，目录、ID和权限不变。

## 安装与更新

推荐从仓库根目录运行 `./install.sh`，具体步骤见[一键安装说明](../README.md#一键安装)。技能安装到通用 `~/.agents/skills/`；Linux 当前会话没有 DISPLAY/WAYLAND_DISPLAY 时默认 headless，跳过扩展和 Native Messaging，仍可用已有 CDP；显式 `--extension-id ID` 可强制预配置 Chrome。

以下为手动安装路径。固定扩展目录为 `~/.local/share/x-gateway/extension`；首次在 `chrome://extensions` 自行加载该目录，并从该页面复制实际扩展 ID，下面用 `EXTENSION_ID` 表示。已有安装沿用原目录和 ID，不假定所有机器使用相同 ID；Agent 不能绕过 Chrome 设置页限制。

编译并安装独立 Go 工具 xweb 后，生成 Native Messaging manifest：

```bash
xweb native-manifest --extension-id EXTENSION_ID \
  --binary /absolute/path/xweb > /absolute/path/com.x_gateway.accessctl.json
```

若使用自定义配置目录，为该命令及 credential/targets/fill 命令传同一个 `--data-dir DIR`。生成命令只输出 Chrome manifest，并在该 data-dir 写 0700 的 `xweb-native-host.sh`；shim 用绝对 binary 路径启动 `xweb native-host`，不含凭据。

将 manifest 安装到当前用户 Chrome 的 NativeMessagingHosts 目录：macOS 为 `~/Library/Application Support/Google/Chrome/NativeMessagingHosts`，Linux Chrome 为 `${XDG_CONFIG_HOME:-$HOME/.config}/google-chrome/NativeMessagingHosts`。文件名为 `com.x_gateway.accessctl.json`，host name 为 `com.x_gateway.accessctl`，allowed_origins 仅允许指定扩展 ID，不对网站开放 Native Messaging。[Chrome Native Messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging)

更新 manifest 不会替换已经运行的 native host。扩展重新连接后会读取新 shim；必要时 popup 的“重新连接”可用于排障。xweb 无需独立常驻服务。Native Messaging 的 com.x_gateway.accessctl 与技能目录 accessctl-ssh/web 是稳定安装标识，不对应额外产品入口。

Chrome 启动 native-host 进程，它只在该 Native Messaging 连接存续时提供私有 Unix socket；Chrome 断开后退出并移除自己的 socket。短 socket 路径位于 `/tmp/xweb-UID-DATADIRHASH/`，目录0700、socket0600，长 data-dir 不会突破 macOS Unix socket 路径长度。不保存待重放操作。

## 每次指定字段

先保存一次凭据：

```bash
xweb credential add monitoring --url https://monitor.example.com/login
# 用户主动提供给Agent的用户名/密码可用受控stdin：
xweb credential add monitoring --url https://monitor.example.com/login --stdin
xweb targets
```

stdin 的严格 JSON 只含 username/password；不写 argv、环境变量或明文临时文件。工具负责本机加密保存，不存 selector、流程或成功条件。日常 fill 可按 IP、URL 或名称选择记录，唯一账号直接使用，多账号列出 username 并要求 `--user USER`；add/delete 继续按 NAME 管理，credential 结果仅有 name/origin/username。地址与名称的优先级、重复别名和 `--exact-name` 见[根 README](../README.md#按-ipurl-或名称选凭据)。

targets 为每个 HTTP/HTTPS tab 返回 target_id、origin、profile_id、bridge_id 和 tab_id，不返回页面标题、路径或查询参数。target_id 是不透明的 `extension:短连接ID:TAB`，不能直接使用浏览器工具自己的标签页 ID。连接更换后须重新查询；旧 target 不会自动切到另一页或重放填写。排除 incognito 标签页。

```bash
xweb fill monitoring --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 也可直接按URL选择；多账号时明确用户名：
xweb fill https://monitor.example.com/login --user USER --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
# 只有显式指定按钮才点击：
xweb fill monitoring --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password' \
  --submit-selector 'button[type=submit]'
```

用户名/密码 selector 至少指定一个，支持多步表单先填用户名并点击下一步，再只填密码。默认只填、不点击；有验证码时省略 submit-selector，交回原页面由 Agent 或用户处理。

selector 沿用浏览器 querySelector 的首匹配语义，Agent 应依据实际页面选择明确唯一的 CSS；工具不宣称会检查唯一性。填写前预检指定凭据字段是否存在、可见、未禁用；提交按钮在填写后检查，最多等待一秒让 input/change 渲染启用，然后只点击一次。填值使用原生 setter 并派发 input/change，可适配 React 受控表单。

凭据的精确 origin 必须匹配明确 tab 的 origin；每次写入/点击沿用当前 document 约束，跨域或导航后拒绝旧操作。只操作顶层页面，不支持通用 iframe/Shadow DOM 定位。工具不会清空成功填入的值，不遮罩页面，也不保存认证流程。

返回 `{target_id,filled,submitted}` 仅表示字段填写及可选按钮点击，不能据此断言登录成功。Agent 每次观察实际页面、决定 selector 和后续动作。认证结果不确定时不自动重复提交。

## Playwright/CDP

已开放的 loopback CDP 浏览器无需本扩展：

```bash
xweb targets --cdp http://127.0.0.1:9222
xweb fill monitoring --cdp http://127.0.0.1:9222 --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
```

CDP 未指定 target-id 时，只有一个页面匹配凭据 origin 才可填写；多个匹配页返回 TARGET_AMBIGUOUS。连接既有 context/target，结束时只 detach 自己的 CDP 会话，不关闭浏览器。Agent 可继续在同一页面处理验证码或提交。

## 隔离验证

```bash
go run ./scripts/simple-web-fixture
```

该 fixture 只启动 loopback HTTP 站点，不写生产记录或安装 Chrome。ready JSON 给出 login_url/multi_url/disabled_url/status_url；源码中的固定凭据是公开假值，仅用于本机验收。它验证原生 input/change、用户名事件替换密码 DOM、初始禁用提交按钮填后启用、单字段多步表单及只填不提交；状态端点只返回计数，不反射凭据。
