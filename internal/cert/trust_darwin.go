//go:build darwin

// Package cert macOS 平台的信任存储实现。
// trust_darwin.go 通过 security 命令行工具将 CA 证书安装到/移除自
// macOS 的登录钥匙串（login.keychain），使其成为受信任的根证书。
//
// 设计要点：
//   - 使用 security add-trusted-cert 命令，设置为 trustRoot 信任策略
//   - 针对较新 macOS 版本（keychain 路径解析问题）提供无 -k 参数的备用方案
//   - Remove 通过证书通用名称（CN）匹配删除
//   - IsInstalled 通过 security find-certificate 检查证书是否存在
package cert

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// platformTrustStore 是 macOS 平台的信任存储实现（空结构体，无状态）。
type platformTrustStore struct{}

// loginKeychain 是用户登录钥匙串的默认路径。
const loginKeychain = "~/Library/Keychains/login.keychain-db"

// Install 将 CA 证书添加到 macOS 登录钥匙串，并设为受信任的根证书。
// 使用 security add-trusted-cert 命令，-d 表示添加到 admin cert store，
// -r trustRoot 表示设为根证书信任策略。
// 如果带 -k 参数的方案失败（较新 macOS 路径解析问题），回退到不带 -k 的版本。
func (platformTrustStore) Install(caPath string) error {
	out, err := exec.Command("security",
		"add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", loginKeychain, caPath).CombinedOutput()
	if err != nil {
		// 回退：在某些较新的 macOS 上 -k 标志的路径解析可能失败
		out2, err2 := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", caPath).CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("security add-trusted-cert failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		_ = out2
	}
	return nil
}

// Remove 通过通用名称（CN）从登录钥匙串中删除 CA 证书。
func (platformTrustStore) Remove(caPath string) error {
	cn, err := commonNameFromFile(caPath)
	if err != nil {
		return err
	}
	out, err := exec.Command("security", "delete-certificate", "-c", cn, loginKeychain).CombinedOutput()
	if err != nil {
		return fmt.Errorf("security delete-certificate failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsInstalled 检查 CA 证书是否存在于登录钥匙串中。
func (platformTrustStore) IsInstalled(caPath string) (bool, error) {
	cn, err := commonNameFromFile(caPath)
	if err != nil {
		return false, err
	}
	homeKeychain := expandHome(loginKeychain)
	out, err := exec.Command("security", "find-certificate", "-c", cn, homeKeychain).CombinedOutput()
	if err != nil {
		return false, nil // 找不到证书视为未安装
	}
	return len(out) > 0, nil
}

// expandHome 展开路径中的 ~/ 前缀为实际的家目录路径。
func expandHome(p string) string {
	home, _ := exec.Command("sh", "-c", "echo $HOME").Output()
	return strings.Replace(p, "~/", strings.TrimSpace(string(home))+string(filepath.Separator), 1)
}
