#!/usr/bin/env bash
# package-linux.sh — 把构建出的 WProxyman 二进制打包为 .tar.gz / .deb / .rpm
# 三件套，覆盖所有主流 Linux 发行版：
#   - .tar.gz   通用手动安装（Arch 等无包管理器格式的发行版）
#   - .deb      Debian / Ubuntu / 其衍生版
#   - .rpm      Fedora / RHEL / CentOS / 其衍生版
#
# 用法：package-linux.sh <binary-path> <version> <out-dir>
#   binary-path  构建产物二进制（如 build/bin/WProxyman）
#   version      版本号，无 v 前缀（如 1.0.5）
#   out-dir      输出目录（放置三个安装包）
set -euo pipefail

BIN="${1:?missing binary path}"
VERSION="${2:?missing version}"
OUT="${3:?missing out dir}"
APP="wproxyman"
ARCH="amd64"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$OUT"
INSTALL_DIR="$STAGE/install/usr/bin"
SHARE_DIR="$STAGE/install/usr/share/applications"
ICO_DIR="$STAGE/install/usr/share/icons/hicolor/256x256/apps"
mkdir -p "$INSTALL_DIR" "$SHARE_DIR" "$ICO_DIR"

# --- 准备安装树：二进制 + .desktop + 图标 ---
cp "$BIN" "$INSTALL_DIR/$APP"
chmod 755 "$INSTALL_DIR/$APP"
SOURCE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cp "$SOURCE_DIR/build/appicon-rounded.png" "$ICO_DIR/$APP.png"
cat > "$SHARE_DIR/$APP.desktop" <<EOF
[Desktop Entry]
Name=WProxyman
Comment=HTTP/HTTPS debugging proxy
Exec=${APP}
Icon=${APP}
Terminal=false
Type=Application
Categories=Development;
EOF

# --- 1. tar.gz：结构为 usr/...，解压到根目录即完成安装 ---
TAR_NAME="${APP}_${VERSION}_linux_${ARCH}.tar.gz"
tar -C "$STAGE/install" -czf "$OUT/$TAR_NAME" .
echo "built: $OUT/$TAR_NAME"

# --- 2. .deb：dpkg-deb 打包（Ubuntu runner 自带）---
DEB_NAME="${APP}_${VERSION}_${ARCH}.deb"
DEB_DIR="$STAGE/deb"
mkdir -p "$DEB_DIR/DEBIAN"
cat > "$DEB_DIR/DEBIAN/control" <<EOF
Package: ${APP}
Version: ${VERSION}
Section: net
Priority: optional
Architecture: ${ARCH}
Maintainer: Wind <xjqxzmxl@gmail.com>
Description: HTTP/HTTPS debugging proxy
 Cross-platform HTTP/HTTPS traffic inspection and debugging tool.
 Homepage: https://github.com/xjqxz2/wproxyman
Depends: libgtk-3-0, libwebkit2gtk-4.0-37
EOF
cp -r "$STAGE/install/usr" "$DEB_DIR/"
dpkg-deb --build --root-owner-group "$DEB_DIR" "$OUT/$DEB_NAME"
echo "built: $OUT/$DEB_NAME"

# --- 3. .rpm：rpmbuild 打包（rpm 架构名为 x86_64，与 deb 的 amd64 不同）---
RPM_ARCH="x86_64"
RPM_NAME="${APP}-${VERSION}-1.${RPM_ARCH}.rpm"
RPM_DIR="$STAGE/rpmbuild"
mkdir -p "$RPM_DIR/BUILD" "$RPM_DIR/RPMS" "$RPM_DIR/SOURCES" "$RPM_DIR/SPECS" "$RPM_DIR/SRPMS"
mkdir -p "$RPM_DIR/SOURCES/usr/bin" "$RPM_DIR/SOURCES/usr/share/applications" "$RPM_DIR/SOURCES/usr/share/icons/hicolor/256x256/apps"
cp "$INSTALL_DIR/$APP" "$RPM_DIR/SOURCES/usr/bin/$APP"
cp "$ICO_DIR/$APP.png" "$RPM_DIR/SOURCES/usr/share/icons/hicolor/256x256/apps/$APP.png"
cp "$SHARE_DIR/$APP.desktop" "$RPM_DIR/SOURCES/usr/share/applications/$APP.desktop"
cat > "$RPM_DIR/SPECS/$APP.spec" <<EOF
Name: ${APP}
Version: ${VERSION}
Release: 1
Summary: HTTP/HTTPS debugging proxy
License: Apache-2.0
URL: https://github.com/xjqxz2/wproxyman
Requires: gtk3, webkit2gtk4.0
BuildArch: ${RPM_ARCH}

%description
Cross-platform HTTP/HTTPS traffic inspection and debugging tool.

%install
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/usr/share/applications
mkdir -p %{buildroot}/usr/share/icons/hicolor/256x256/apps
cp -r %{_sourcedir}/usr/* %{buildroot}/usr/

%files
/usr/bin/${APP}
/usr/share/applications/${APP}.desktop
/usr/share/icons/hicolor/256x256/apps/${APP}.png
EOF
rpmbuild --define "_topdir $RPM_DIR" -bb "$RPM_DIR/SPECS/$APP.spec" >/dev/null
cp "$RPM_DIR/RPMS/$RPM_ARCH/$RPM_NAME" "$OUT/$RPM_NAME"
echo "built: $OUT/$RPM_NAME"

echo "ALL_LINUX_PACKAGES"