//go:build linux

// Package cert Linux 平台的信任存储实现。
// trust_linux.go 将 CA 证书复制到系统 CA 证书目录并更新信任存储，
// 支持多种主流 Linux 发行版。
//
// 设计要点：
//   - 自动检测系统 CA 目录（按优先级）：Debian/Ubuntu、Fedora/RHEL、Arch、通用回退
//   - 将 CA 证书复制到系统锚点目录（需要 root 权限）
//   - 使用发行版特定的更新命令（update-ca-certificates / update-ca-trust / trust extract-compat）
//   - Remove 遍历所有已知目录查找并删除锚点文件
//   - IsInstalled 通过检查锚点文件是否存在来判断
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

// linuxTrustDirs 返回当前系统上存在的 CA 存储目录及其对应的更新命令。
// 按优先级排列：Debian/Ubuntu → Fedora/RHEL → Arch → 通用回退。
// 只返回实际存在的目录。
func linuxTrustDirs() []struct {
	dir    string
	update string
} {
	// 候选目录列表（按发行版优先级排列）
	candidates := []struct {
		dir    string
		update string
	}{
		{"/usr/local/share/ca-certificates", "update-ca-certificates"},           // Debian/Ubuntu
		{"/etc/pki/ca-trust/source/anchors", "update-ca-trust"},                  // Fedora/RHEL
		{"/etc/ca-certificates/trust-source/anchors", "trust extract-compat"},    // Arch
		{"/etc/ssl/certs", "update-ca-certificates"},                             // 通用回退
	}
	var existing []struct {
		dir    string
		update string
	}
	for _, c := range candidates {
		if st, err := os.Stat(c.dir); err == nil && st.IsDir() {
			existing = append(existing, c)
		}
	}
	return existing
}

// Install 将 CA 证书复制到系统 CA 目录并运行更新命令。
// 使用第一个匹配的发行版目录。需要 root 权限才能写入系统目录。
func (platformTrustStore) Install(caPath string) error {
	dirs := linuxTrustDirs()
	if len(dirs) == 0 {
		return fmt.Errorf("no known system CA directory found; install the certificate manually")
	}
	// 使用第一个匹配的发行版系列
	d := dirs[0]
	// 将证书复制到系统锚点目录，文件名为 wproxyman-ca.crt
	dst := filepath.Join(d.dir, "wproxyman-ca.crt")
	data, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("writing %s failed (requires root?): %v", dst, err)
	}
	// 运行发行版特定的更新命令以重新加载信任存储
	if d.update != "" {
		out, err := exec.Command(d.update).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s failed: %v: %s", d.update, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Remove 从所有已知的 CA 目录中删除 WProxyman 的 CA 锚点文件。
// 找到并删除第一个存在的文件后返回，同时运行对应的更新命令。
func (platformTrustStore) Remove(caPath string) error {
	dirs := linuxTrustDirs()
	for _, d := range dirs {
		anchor := filepath.Join(d.dir, "wproxyman-ca.crt")
		if _, err := os.Stat(anchor); err == nil {
			if err := os.Remove(anchor); err != nil {
				return err
			}
			// 如果找到并删除了文件，运行更新命令（忽略错误，尽力而为）
			if d.update != "" {
				_ = exec.Command(d.update).Run()
			}
			return nil
		}
	}
	return nil // 文件不存在，视为已删除
}

// IsInstalled 尽力检查 CA 证书是否已安装到系统信任存储。
// 遍历标准锚点目录，查找 wproxyman-ca.crt 文件。
func (platformTrustStore) IsInstalled(caPath string) (bool, error) {
	// 尽力而为：检查标准锚点目录
	names := []string{"wproxyman-ca.crt"}
	for _, d := range linuxTrustDirs() {
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(d.dir, n)); err == nil {
				return true, nil
			}
		}
	}
	return false, nil
}
