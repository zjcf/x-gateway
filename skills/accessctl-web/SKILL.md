---
name: accessctl-web
description: 当用户需要登录网站或设备管理页面、填写登录表单，或管理网站凭据时使用。通过 xweb 复用已保存账号并按需提交；登录后的一般网页操作由浏览器工具完成。
---

## 工具依赖

使用前须安装外部 x-gateway 工具 `xweb` 并加入 PATH；安装技能不会安装工具或浏览器连接配置。Chrome 扩展或已有 CDP 的接入方式见 [x-gateway README](https://github.com/zjcf/x-gateway/blob/main/README.md)。

## 选择页面并填写登录表单

先用浏览器工具观察当前页面，明确目标页面、账号及能定位登录字段的 CSS selector，再查询可用页面：

```sh
xweb targets
xweb fill https://example.com/login --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
```

`TARGET_ID` 必须使用本次 `targets` 返回的完整 opaque `target_id`，不能使用 Chrome tab 数字或其他浏览器工具的标签 ID。多个同 origin 页面须明确选择，不猜第一项或活动页；连接变化后重新查询。

`fill SITE` 的 SITE 可使用 IP、URL 或网站名称 NAME，不要求用户记住名称：

- 明确 URL 或 IP 按地址查找；URL 只按 scheme/host/port 的 origin 匹配，忽略路径、查询和片段。裸主机匹配多个 origin 时，按实际页面给完整 HTTP(S) URL，不猜协议。
- 普通文本先精确匹配 NAME，未命中再按 host 查找。名称看起来像 IP/URL 或同账号跨名称重复时，用已选元数据中的 NAME 和 `--exact-name` 明确记录。
- 选定网站后唯一账号可复用；多账号须用 `--user USER`，保留用户名大小写。空用户名、无匹配或选择歧义不回退、不填写，也不任意选第一条。必要时用 `xweb credential list` 查看 name/origin/username 元数据。

单字段或多步登录可分次填写，至少指定用户名或密码 selector 之一：

```sh
xweb fill NAME --user USER --target-id TARGET_ID --password-selector '#password'
xweb fill NAME --exact-name --user USER --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
```

默认只填写，不提交、不清空或遮罩字段。仅用户任务要求提交且已明确按钮时，追加 `--submit-selector '#login'`；提交最多点击一次，不自动重试认证。

已有 CDP 浏览器使用同一 endpoint 查询和填写：

```sh
xweb targets --cdp http://127.0.0.1:9222
xweb fill NAME --cdp http://127.0.0.1:9222 --target-id TARGET_ID \
  --username-selector '#username' --password-selector '#password'
```

CDP endpoint 仅接受 loopback HTTP/WebSocket；省略 `--target-id` 只在唯一一个与凭据 origin 匹配的 page 时允许。`xweb` 不启动或关闭浏览器。

凭据仅向登记 URL 的精确 scheme/host/port 注入，填值与点击还验证同一 URL/document；不能为跨域登录降级 origin 检查。selector 使用 `querySelector` 首匹配，不保证唯一；只支持顶层普通 input/textarea，不承诺 iframe、Shadow DOM 或 SSO 弹窗。

填写派发 input/change 事件，字段保留供页面使用；页面脚本及有页面权限的 Agent 可读取填值，不为无关目的读取或打印密码。CLI 结果不包含注入值。

`filled` 只表示填值完成，`submitted` 只表示点击过指定按钮，均不证明登录成功。随后用浏览器工具观察页面，确认登录结果并处理后续步骤。验证码遵循实际浏览器工具当次操作规则；MFA/OTP 确需用户输入时请求用户录入，不承诺绕过验证。

失败不盲目重试或反复提交；错误或取消可能留下部分填值，也不能撤回已提交请求，不宣称字段已清理。登录后继续使用原浏览器页面完成用户任务。

## 首次保存或更新凭据

没有匹配记录时，仅保存用户本次主动提供或明确指定来源的网站账号，不搜索 Chrome 密码库、其他秘密或其他聊天：

```sh
xweb credential add NAME --url https://example.com/login --stdin
```

以真实 PTY 启动，等待诊断流输出以下 ready 消息，工具关闭回显后才通过该进程 stdin 写入一行序列化 JSON 和换行：

```json
{"operation":"credential.add","ready":true,"input":"hidden_json_line"}
```

JSON 仅允许非空 `username` / `password`，最多 1 MiB。用序列化处理引号及非 ASCII；不要提前输入，不把秘密拼进 shell、argv、环境变量、日志或明文临时文件。没有可靠受控 stdin 时，让用户在自己的终端运行不带 `--stdin` 的同一命令，隐藏录入。

`add` 按 NAME + 输入的 username 保存；同账号完整替换，不同账号互不覆盖，用户名不是通过 `--user` 传入。更新时重新提供用户名与密码，不从库内读回补齐。同 NAME 下各账号须属于同一 origin，不一致时使用不同名称，不放宽绑定。

使用 `--data-dir ROOT` 时，各次操作保持同一目录，实际凭据位于 `ROOT/simple`。文件加密不能抵御同 OS 用户读取本机 key 后解密；CLI 不提供认证值的读取或导出入口。

## 查询、删除与轮换

```sh
xweb credential list
xweb credential delete NAME [--user USER]
xweb credential rotate
```

`list`、`add` 和 `delete` 的成功记录仅含 name/origin/username，不含认证秘密。`add` / `delete` 精确使用 NAME，不按 IP/URL 扩大选择范围；删除多账号网站须明确 `--user`，唯一账号可省略。删除不撤销已有 Cookie。

`rotate` 轮换共享库数据 key，重加密全部 SSH 和 Web 凭据，不修改业务账号密码，`machine.key` 不变；日常填写无需执行。
