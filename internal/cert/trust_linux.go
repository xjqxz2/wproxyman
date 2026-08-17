//go:build linux

// Package cert Linux 平台的信任存储实现。
// trust_linux.go 将 CA 证书加入系统信任存储，支持多种主流发行版。
//
// 设计要点：
//   - 自动检测系统 CA 目录（Debian/Ubuntu、Fedora/RHEL、Arch）；
//   - 写入系统目录需要 root：失败时返回**可直接执行的 sudo 指引**，
//     用户复制执行后应用即可检测到（见 IsInstalled 的 bundle 检查）；
//   - IsInstalled 双重检测：① anchor 文件存在 ② 系统 CA bundle 中已合并
//     （最可靠——bundle 包含即表示系统真正信任）；
//   - Remove 删除 anchor 并刷新。
package cert

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// platformTrustStore 是 Linux 平台的信任存储实现（空结构体，无状态）。
type platformTrustStore struct{}

// trustDir 描述一个系统 CA 目录及其刷新命令。
type trustDir struct {
	dir    string
	update string
}

// linuxTrustDirs 返回当前系统上存在的 CA 存储目录（按发行版优先级）。
func linuxTrustDirs() []trustDir {
	candidates := []trustDir{
		{"/usr/local/share/ca-certificates", "update-ca-certificates"}, // Debian/Ubuntu
		{"/etc/pki/ca-trust/source/anchors", "update-ca-trust"},        // Fedora/RHEL
		{"/etc/ca-certificates/trust-source/anchors", "trust extract-compat"}, // Arch
	}
	var existing []trustDir
	for _, c := range candidates {
		if st, err := os.Stat(c.dir); err == nil && st.IsDir() {
			existing = append(existing, c)
		}
	}
	return existing
}

// caBundles 返回各发行版合并后的系统 CA bundle 路径（用于可靠检测）。
func caBundles() []string {
	return []string{
		"/etc/ssl/certs/ca-certificates.crt",                    // Debian/Ubuntu
		"/etc/pki/tls/certs/ca-bundle.crt",                      // Fedora/RHEL
		"/etc/ca-certificates/extracted/tls-ca-bundle.pem",      // Arch
	}
}

// bundleContainsCN 检查某个 CA bundle 文件中是否包含指定的 CN 文本。
func bundleContainsCN(bundle, cn string) bool {
	data, err := os.ReadFile(bundle)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), cn)
}

// Install 将 CA 证书加入系统信任存储。
// 写入系统目录需要 root：若权限不足，返回带可执行 sudo 命令的指引。
func (platformTrustStore) Install(caPath string) error {
	dirs := linuxTrustDirs()
	if len(dirs) == 0 {
		return fmt.Errorf("未找到已知的系统 CA 目录，请手动安装证书")
	}
	d := dirs[0]
	dst := filepath.Join(d.dir, "wproxyman-ca.crt")
	data, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		// 权限不足：给出可直接执行的 sudo 指引（用户复制执行后应用自动识别）。
		cmd := fmt.Sprintf("sudo cp %s %s && sudo %s", caPath, dst, d.update)
		return fmt.Errorf("写入系统证书目录需要 root 权限。请在终端执行以下命令后返回本应用重新检测：\n%s", cmd)
	}
	// 刷新信任存储（同样可能需 root）。
	if d.update != "" {
		out, err := exec.Command(d.update).CombinedOutput()
		if err != nil {
			cmd := fmt.Sprintf("sudo %s", d.update)
			return fmt.Errorf("刷新证书存储需要 root 权限。请在终端执行：\n%s\n（%v）", cmd, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Remove 删除 anchor 文件并刷新信任存储。
func (platformTrustStore) Remove(caPath string) error {
	dirs := linuxTrustDirs()
	for _, d := range dirs {
		anchor := filepath.Join(d.dir, "wproxyman-ca.crt")
		if _, err := os.Stat(anchor); err == nil {
			if err := os.Remove(anchor); err != nil {
				return err
			}
			if d.update != "" {
				_ = exec.Command(d.update).Run()
			}
			return nil
		}
	}
	return nil // 文件不存在，视为已删除
}

// IsInstalled 检测 CA 证书是否已被系统信任。
// 双重检测：
//  1. anchor 文件是否存在于标准目录；
//  2. 系统 CA bundle 中是否已合并该证书（update 命令生成，最可靠——
//     无论用户通过应用、sudo cp 还是其他方式安装都能识别）。
func (platformTrustStore) IsInstalled(caPath string) (bool, error) {
	// 1. anchor 文件
	for _, d := range linuxTrustDirs() {
		if _, err := os.Stat(filepath.Join(d.dir, "wproxyman-ca.crt")); err == nil {
			return true, nil
		}
	}
	// 2. 系统 CA bundle 中已合并（真正被信任）
	cn, err := commonNameFromFile(caPath)
	if err != nil {
		return false, nil
	}
	for _, bundle := range caBundles() {
		if bundleContainsCN(bundle, cn) {
			return true, nil
		}
	}
	return false, nil
}
