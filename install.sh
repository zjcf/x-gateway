#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
用法：
  ./install.sh  # 安装工具和通用skills；有图形会话时交互配置Chrome
  ./install.sh --extension-id ID  # 使用已有的32位扩展ID无人值守安装，并显式配置Chrome
  ./install.sh -h  # 显示帮助
  ./install.sh --help  # 显示帮助

需要macOS或Linux、Go 1.25+；Chrome模式需Google Chrome，不会自动安装依赖。
工具安装到~/.local/bin，skills固定安装到~/.agents/skills。
macOS默认配置Chrome；Linux无DISPLAY/WAYLAND_DISPLAY时跳过Chrome，无需TTY或扩展ID。
显式--extension-id始终配置Chrome，扩展目录为~/.local/share/x-gateway/extension。
保留全部已有凭据，不初始化或轮换凭据，不安装常驻服务。
EOF
}
fail() { printf '安装失败：%s\n' "$*" >&2; return 1; }
valid_extension_id() { [[ "$1" =~ ^[a-p]{32}$ ]]; }
chrome_enabled() {
  [[ -n "$4" || "$1" == Darwin || "$1" == Linux && ( -n "$2" || -n "$3" ) ]]
}

atomic_copy() {
  local source_file="$1" destination="$2" mode="$3" staging
  [[ ! -d "$destination" ]] || { fail "目标是目录：$destination"; return 1; }
  mkdir -p "$(dirname "$destination")"
  staging=$(mktemp "$(dirname "$destination")/.x-gateway-install.XXXXXX")
  if ! cp "$source_file" "$staging" || ! chmod "$mode" "$staging" || ! mv -f "$staging" "$destination"; then
    rm -f "$staging"
    fail "无法原子安装：$destination"
    return 1
  fi
}
install_tree() {
  local source_dir="$1" destination_dir="$2" source_file relative
  mkdir -p "$destination_dir"
  while IFS= read -r -d '' source_file; do
    relative=${source_file#"$source_dir"/}
    atomic_copy "$source_file" "$destination_dir/$relative" 644
  done < <(find "$source_dir" -type f -print0)
}
append_path() {
  local rc_file="$1" line='export PATH="$HOME/.local/bin:$PATH"'
  mkdir -p "$(dirname "$rc_file")"
  if [[ -f "$rc_file" ]] && grep -Fqx "$line" "$rc_file"; then return; fi
  printf '\n# x-gateway: 让xssh/xweb命令在新终端中可用\n%s\n' "$line" >> "$rc_file"
}
configure_path() {
  local platform="$1" bin_dir="$2" rc_file current_shell="${SHELL:-}"
  case ":${PATH:-}:" in *":$bin_dir:"*) return ;; esac
  case "${current_shell##*/}" in
    zsh) rc_file="${ZDOTDIR:-$HOME}/.zshrc" ;;
    bash)
      if [[ "$platform" == Darwin ]]; then rc_file="$HOME/.bash_profile"; else rc_file="$HOME/.bashrc"; fi ;;
    *) printf '当前shell未识别；请将 %s 加入PATH后使用命令。\n' "$bin_dir"; return ;;
  esac
  append_path "$rc_file"
  printf 'PATH已配置：%s；新终端生效。当前终端可直接使用 %s/xssh 和 %s/xweb。\n' "$rc_file" "$bin_dir" "$bin_dir"
}
install_manifest() {
  local binary="$1" extension_id="$2" destination="$3" staging
  [[ ! -d "$destination" ]] || { fail "manifest目标是目录"; return 1; }
  mkdir -p "$(dirname "$destination")"
  staging=$(mktemp "$(dirname "$destination")/.x-gateway-manifest.XXXXXX")
  if ! "$binary" native-manifest --extension-id "$extension_id" --binary "$binary" > "$staging"; then
    rm -f "$staging"
    fail "连接配置生成失败，原manifest保留"
    return 1
  fi
  if ! chmod 600 "$staging" || ! mv -f "$staging" "$destination"; then
    rm -f "$staging"
    fail "连接配置安装失败，原manifest保留"
    return 1
  fi
}

main() {
  local extension_id='' platform version go_major go_minor host_os host_arch build_os build_arch
  local repo_dir bin_dir skills_dir extension_dir manifest_dir work_dir chrome_mode=no
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) usage; return ;;
      --extension-id)
        [[ $# -ge 2 && -z "$extension_id" ]] || { fail "--extension-id需要唯一ID"; return 1; }
        extension_id="$2"
        valid_extension_id "$extension_id" || { fail "扩展ID必须是32位a-p字符"; return 1; }
        shift 2 ;;
      *) fail "未知参数：$1"; return 1 ;;
    esac
  done
  platform=$(uname -s)
  [[ "$platform" == Darwin || "$platform" == Linux ]] || { fail "仅支持macOS和Linux"; return 1; }
  if chrome_enabled "$platform" "${DISPLAY:-}" "${WAYLAND_DISPLAY:-}" "$extension_id"; then chrome_mode=yes; fi
  command -v go >/dev/null 2>&1 || { fail "请先安装Go 1.25或更高版本"; return 1; }
  version=$(GOTOOLCHAIN=local go env GOVERSION)
  [[ "$version" =~ ^go([0-9]+)\.([0-9]+) ]] || { fail "无法识别Go版本：$version"; return 1; }
  go_major=${BASH_REMATCH[1]}; go_minor=${BASH_REMATCH[2]}
  (( go_major > 1 || go_major == 1 && go_minor >= 25 )) || { fail "需要Go 1.25或更高版本，当前为$version"; return 1; }
  host_os=$(GOTOOLCHAIN=local go env GOHOSTOS); host_arch=$(GOTOOLCHAIN=local go env GOHOSTARCH)
  build_os=$(GOTOOLCHAIN=local go env GOOS); build_arch=$(GOTOOLCHAIN=local go env GOARCH)
  [[ "$host_os" == "$build_os" && "$host_arch" == "$build_arch" ]] || { fail "GOOS/GOARCH必须与当前机器一致，不能安装交叉编译产物"; return 1; }
  [[ "$chrome_mode" == no || -n "$extension_id" || -t 0 ]] || { fail "Chrome模式的非交互安装请提供--extension-id ID"; return 1; }
  repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
  bin_dir="$HOME/.local/bin"
  skills_dir="$HOME/.agents/skills"
  extension_dir="$HOME/.local/share/x-gateway/extension"
  if [[ "$platform" == Darwin ]]; then
    manifest_dir="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
  else
    manifest_dir="${XDG_CONFIG_HOME:-$HOME/.config}/google-chrome/NativeMessagingHosts"
  fi
  work_dir=$(mktemp -d "${TMPDIR:-/tmp}/x-gateway-install.XXXXXX")
  trap "rm -rf -- $(printf '%q' "$work_dir")" EXIT
  printf '正在构建xssh和xweb……\n'
  (
    cd "$repo_dir"
    GOTOOLCHAIN=local CGO_ENABLED=0 go build -o "$work_dir/xssh" ./cmd/xssh
    GOTOOLCHAIN=local CGO_ENABLED=0 go build -o "$work_dir/xweb" ./cmd/xweb
  )
  atomic_copy "$work_dir/xssh" "$bin_dir/xssh" 755
  atomic_copy "$work_dir/xweb" "$bin_dir/xweb" 755
  install_tree "$repo_dir/skills/accessctl-ssh" "$skills_dir/accessctl-ssh"
  install_tree "$repo_dir/skills/accessctl-web" "$skills_dir/accessctl-web"
  if [[ "$chrome_mode" == yes ]]; then install_tree "$repo_dir/extension" "$extension_dir"; fi
  configure_path "$platform" "$bin_dir"
  if [[ "$chrome_mode" == yes && -z "$extension_id" ]]; then
    printf '\n请在Chrome的chrome://extensions开启开发者模式并加载已解压目录：\n%s\n' "$extension_dir"
    printf '如果已加载这个目录，扩展文件有更新时点击“重新加载”，沿用原扩展ID，无需重复添加。\n'
    while :; do
      printf '请粘贴32位扩展ID：' >&2
      IFS= read -r extension_id || { fail "未收到扩展ID；已有文件保留，可用--extension-id重试"; return 1; }
      valid_extension_id "$extension_id" && break
      printf 'ID无效，需要32位a-p字符。\n' >&2
    done
  fi
  if [[ "$chrome_mode" == yes ]]; then install_manifest "$bin_dir/xweb" "$extension_id" "$manifest_dir/com.x_gateway.accessctl.json"; fi
  printf '\n安装完成。命令目录：%s\nskills目录：%s\n' "$bin_dir" "$skills_dir"
  if [[ "$chrome_mode" == yes ]]; then
    printf 'Chrome扩展目录：%s\n连接配置：%s\n' "$extension_dir" "$manifest_dir/com.x_gateway.accessctl.json"
  else
    printf 'Linux无图形会话：已跳过Chrome扩展和Native Messaging配置，xweb仍可使用CDP。\n'
  fi
  printf '工具直接运行，无需常驻服务；已有凭据保持不变。\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
