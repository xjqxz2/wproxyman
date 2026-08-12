#!/usr/bin/env bash
# ============================================================================
# macOS 本地 ad-hoc 打包脚本（个人自用，无需 Apple Developer 证书）
#
# 流程：构建（本机架构）→ ad-hoc 签名 → 打成 .dmg（含 Applications 拖拽位）
# 产物：WProxyman_darwin_<架构>.dmg（在项目根目录）
#
# 用法（在项目根目录）：
#   bash scripts/build-macos.sh
#   # 或：make dmg
# ============================================================================
set -euo pipefail
cd "$(dirname "$0")/.."

# 检测本机架构：arm64（Apple Silicon）或 amd64（Intel）
ARCH="$(uname -m)"          # arm64 / x86_64
GOARCH="$ARCH"
[ "$GOARCH" = "x86_64" ] && GOARCH="amd64"

echo "==> 构建 WProxyman（darwin/$GOARCH）"
wails build -platform "darwin/$GOARCH"

APP="build/bin/WProxyman.app"
[ -d "$APP" ] || { echo "错误：未找到 $APP，构建可能失败"; exit 1; }

echo "==> ad-hoc 签名"
codesign --force --deep --sign - "$APP"

echo "==> 打包 dmg"
DMG_STAGE="$(mktemp -d)"
trap 'rm -rf "$DMG_STAGE"' EXIT
cp -R "$APP" "$DMG_STAGE/"
ln -s /Applications "$DMG_STAGE/Applications"

OUT="WProxyman_darwin_${ARCH}.dmg"
hdiutil create \
  -volname WProxyman \
  -srcfolder "$DMG_STAGE" \
  -ov -format UDZO \
  "$OUT"

echo "==> 完成：$OUT"
echo
echo "首次打开提示（macOS Gatekeeper）处理方式："
echo "  1. 右键点击 WProxyman.app → 打开；或"
echo "  2. xattr -d com.apple.quarantine /Applications/WProxyman.app"
