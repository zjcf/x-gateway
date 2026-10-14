#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
用法：
  ./uninstall.sh  # 卸载xssh/xweb、两份skills、扩展文件和Native Messaging配置
  ./uninstall.sh -h  # 显示帮助
  ./uninstall.sh --help  # 显示帮助

仅支持macOS和Linux，不需要Go或已安装的工具。
skills固定从~/.agents/skills卸载；无图形会话时仍清理本工具的Chrome残留配置。
保留全部凭据、machine key、SSH主机密钥记录及其他用户数据。
保留共享父目录与PATH配置；不提供删除凭据的选项，不终止现有进程。
Chrome中已加载的扩展条目需要用户手动移除。
EOF
}
fail() { printf '卸载失败：%s\n' "$*" >&2; return 1; }
validate_root() {
  local root="$1"
  [[ "$root" == /* && "$root" != / ]] || { fail "安装根目录必须为非空绝对路径且不能是/"; return 1; }
  case "$root/" in *'/../'*|*'/./'*) fail "安装根目录不能包含相对路径段"; return 1 ;; esac
}
validate_leaf() {
  local path="$1" expected="$2"
  validate_root "$path"
  [[ "${path##*/}" == "$expected" ]] || { fail "拒绝非固定卸载目标"; return 1; }
}
check_file() {
  local path="$1" expected="$2"
  case "$expected" in xssh|xweb|com.x_gateway.accessctl.json|xweb-native-host.sh) ;; *) fail "拒绝非固定文件目标"; return 1 ;; esac
  validate_leaf "$path" "$expected"
  [[ -L "$path" || ! -e "$path" || -f "$path" ]] || { fail "文件位置存在异常目录或特殊文件：$path"; return 1; }
}
check_tree() {
  local path="$1" expected="$2"
  case "$expected" in accessctl-ssh|accessctl-web|extension) ;; *) fail "拒绝非固定目录目标"; return 1 ;; esac
  validate_leaf "$path" "$expected"
  [[ -L "$path" || ! -e "$path" || -d "$path" ]] || { fail "目录位置存在异常文件：$path"; return 1; }
}
remove_file() {
  check_file "$1" "$2"
  rm -f -- "$1"
}
remove_tree() {
  check_tree "$1" "$2"
  if [[ -L "$1" ]]; then
    rm -f -- "$1"
  elif [[ -d "$1" ]]; then
    rm -rf -- "$1"
  fi
}

# Explicit roots make isolated tests possible without replacing HOME/CODEX_HOME.
uninstall_paths() {
  local bin_dir="$1" skills_dir="$2" extension_dir="$3" manifest_dir="$4" data_dir="$5" root
  for root in "$bin_dir" "$skills_dir" "$extension_dir" "$manifest_dir" "$data_dir"; do validate_root "$root"; done
  # Complete the entire plan's validation before the first removal.
  check_file "$bin_dir/xssh" xssh
  check_file "$bin_dir/xweb" xweb
  check_file "$manifest_dir/com.x_gateway.accessctl.json" com.x_gateway.accessctl.json
  check_file "$data_dir/xweb-native-host.sh" xweb-native-host.sh
  check_tree "$skills_dir/accessctl-ssh" accessctl-ssh
  check_tree "$skills_dir/accessctl-web" accessctl-web
  check_tree "$extension_dir" extension
  remove_file "$bin_dir/xssh" xssh
  remove_file "$bin_dir/xweb" xweb
  remove_file "$manifest_dir/com.x_gateway.accessctl.json" com.x_gateway.accessctl.json
  remove_file "$data_dir/xweb-native-host.sh" xweb-native-host.sh
  remove_tree "$skills_dir/accessctl-ssh" accessctl-ssh
  remove_tree "$skills_dir/accessctl-web" accessctl-web
  remove_tree "$extension_dir" extension
}

main() {
  local platform data_dir manifest_dir
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) usage; return ;;
      *) fail "未知参数；仅支持-h或--help"; return 1 ;;
    esac
  done
  platform=$(uname -s)
  case "$platform" in
    Darwin)
      data_dir="${HOME:-}/Library/Application Support/x-gateway"
      manifest_dir="${HOME:-}/Library/Application Support/Google/Chrome/NativeMessagingHosts" ;;
    Linux)
      data_dir="${XDG_CONFIG_HOME:-${HOME:-}/.config}/x-gateway"
      manifest_dir="${XDG_CONFIG_HOME:-${HOME:-}/.config}/google-chrome/NativeMessagingHosts" ;;
    *) fail "仅支持macOS和Linux"; return 1 ;;
  esac
  validate_root "${HOME:-}"
  uninstall_paths "$HOME/.local/bin" "$HOME/.agents/skills" "$HOME/.local/share/x-gateway/extension" "$manifest_dir" "$data_dir"
  printf '卸载完成。已保留全部凭据、其他用户数据、共享父目录和PATH配置。\n'
  printf '请在chrome://extensions手动移除已加载的x-gateway扩展条目。\n'
  printf '现有SSH进程不会被终止；Chrome native-host会随移除扩展或关闭浏览器而断连结束。\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
