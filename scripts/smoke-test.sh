#!/usr/bin/env bash
# ============================================================================
# macOS / Linux 构建后冒烟测试（make build 完成后自动执行）
#
# 验证项：
#   1. 产物存在且可执行；
#   2. 启动应用，等待其自动开启系统代理；
#   3. 通过代理发送 HTTP 请求（200）；
#   4. 正常关闭应用，验证系统代理被清理。
#
# 任一步失败即退出非零，构建视为失败。
# ============================================================================
set -u
cd "$(dirname "$0")/.."

OS="$(uname -s)"
PROXY_TIMEOUT=30          # 等待系统代理就绪的最长秒数
APP_PID=""

fail() {
  echo "❌ 冒烟测试失败: $1" >&2
  [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null
  exit 1
}

echo "==> 冒烟测试开始（$OS）"

# 1. 产物检查
if [ "$OS" = "Darwin" ]; then
  APP="build/bin/WProxyman.app"
  BIN="$APP/Contents/MacOS/WProxyman"
  [ -d "$APP" ] || fail "未找到 $APP"
  [ -x "$BIN" ] || fail "$BIN 不可执行"
  echo "   [OK] 产物存在: $APP"
  # 启动 .app（GUI）
  open "$APP" || fail "open $APP 失败"
else
  BIN="build/bin/WProxyman"
  [ -x "$BIN" ] || fail "未找到可执行产物 $BIN"
  echo "   [OK] 产物存在: $BIN"
  # 后台启动（Linux 桌面会话下运行）
  "$BIN" &
  APP_PID=$!
fi

# 2. 等待系统代理端口就绪
echo "   [..] 等待系统代理端口..."
PORT=""
for i in $(seq 1 $PROXY_TIMEOUT); do
  sleep 1
  if [ "$OS" = "Darwin" ]; then
    # macOS：读取任一网络服务的 webproxy 端口
    SVC="$(networksetup -listallnetworkservices 2>/dev/null | grep -v '^\*' | head -n1 | tr -d ' ')"
    [ -n "$SVC" ] && PORT="$(networksetup -getwebproxy "$SVC" 2>/dev/null | awk -F': ' '/^Port:/{print $2}')"
  else
    # Linux：GNOME 代理设置（未启用则尝试 KDE 或跳过）
    if command -v gsettings >/dev/null 2>&1; then
      MODE="$(gsettings get org.gnome.system.proxy mode 2>/dev/null | tr -d "'")"
      [ "$MODE" = "manual" ] && PORT="$(gsettings get org.gnome.system.proxy.http port 2>/dev/null | tr -d "'")"
    fi
  fi
  [ -n "$PORT" ] && break
done
if [ -z "$PORT" ]; then
  fail "超时：未检测到系统代理端口（应用可能未启动，或桌面代理未设置）"
fi
echo "   [OK] 系统代理端口 = $PORT"

# 3. HTTP 代理请求（baidu 在国内/国际均可达，避免依赖单一站点）
echo "   [..] HTTP 请求测试..."
HTTP_CODE=""
for url in "http://www.baidu.com/" "http://www.qq.com/"; do
  HTTP_CODE="$(curl -s -o /dev/null -w '%{http_code}' -x "http://127.0.0.1:$PORT" "$url" --max-time 15 2>/dev/null)"
  [ "$HTTP_CODE" = "200" ] && break
  HTTP_CODE=""
done
if [ -z "$HTTP_CODE" ]; then
  fail "HTTP 请求失败（已尝试 baidu/qq）"
fi
echo "   [OK] HTTP -> $HTTP_CODE"

# 4. 关闭应用并验证系统代理清理
if [ "$OS" = "Darwin" ]; then
  osascript -e 'quit app "WProxyman"' >/dev/null 2>&1
  sleep 2
else
  kill "$APP_PID" 2>/dev/null
  wait "$APP_PID" 2>/dev/null
  sleep 1
  APP_PID=""
fi

if [ "$OS" = "Darwin" ]; then
  SVC="$(networksetup -listallnetworkservices 2>/dev/null | grep -v '^\*' | head -n1 | tr -d ' ')"
  STATE="$(networksetup -getwebproxy "$SVC" 2>/dev/null | awk -F': ' '/^Enabled:/{print $2}')"
  [ "$STATE" = "Yes" ] && fail "退出后系统代理未清理"
else
  MODE="$(gsettings get org.gnome.system.proxy mode 2>/dev/null | tr -d "'")"
  [ "$MODE" = "manual" ] && fail "退出后系统代理未清理"
fi
echo "   [OK] 系统代理已清理"

echo "==> 冒烟测试全部通过 ✔"
exit 0
