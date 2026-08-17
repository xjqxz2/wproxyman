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

// Install 将 CA 证书安装为 macOS 系统级受信任根证书。
//
// 优先写入系统钥匙串（Safari / Chrome / 所有浏览器都认系统级信任）；
// 写入系统钥匙串需要管理员授权（首次弹一次密码框，之后证书已存在不再弹）。
// 若系统钥匙串写入失败，回退到登录钥匙串（当前用户级信任）。
func (platformTrustStore) Install(caPath string) error {
	// 1. 系统钥匙串（系统级信任，浏览器通用）
	sysKc := "/Library/Keychains/System.keychain"
	_, err := exec.Command("security",
		"add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", sysKc, caPath).CombinedOutput()
	if err == nil {
		return nil
	}
	// 2. 回退：登录钥匙串（当前用户级信任）
	kc := expandHome(loginKeychain)
	out2, err2 := exec.Command("security",
		"add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", kc, caPath).CombinedOutput()
	if err2 != nil {
		return fmt.Errorf("security add-trusted-cert failed (system & login): %v: %s",
			err2, strings.TrimSpace(string(out2)))
	}
	return nil
}

// Remove 从登录钥匙串和系统钥匙串中删除 CA 证书。
func (platformTrustStore) Remove(caPath string) error {
	cn, err := commonNameFromFile(caPath)
	if err != nil {
		return err
	}
	keychains := []string{
		"/Library/Keychains/System.keychain",
		expandHome(loginKeychain),
	}
	var lastErr error
	for _, kc := range keychains {
		out, err := exec.Command("security", "delete-certificate", "-c", cn, kc).CombinedOutput()
		if err != nil {
			lastErr = err
			continue
		}
		_ = out
	}
	if lastErr != nil {
		return fmt.Errorf("security delete-certificate failed: %v", lastErr)
	}
	return nil
}

// IsInstalled 检查 CA 证书是否已安装（搜索全部钥匙串）。
//
// 注意：证书可能落在登录钥匙串、iCloud 钥匙串或系统钥匙串（取决于用户
// 授权安装时的位置），因此依次尝试：
//  1. 默认搜索列表（login / iCloud / system 等全部钥匙串）
//  2. 登录钥匙串
//  3. 系统钥匙串
// 检测必须可靠——否则应用会误以为未安装而反复触发安装，导致每次启动
// 都弹系统授权框。
func (platformTrustStore) IsInstalled(caPath string) (bool, error) {
	cn, err := commonNameFromFile(caPath)
	if err != nil {
		return false, err
	}
	// 1. 默认搜索列表（login / iCloud / system 等）
	out, err := exec.Command("security", "find-certificate", "-a", "-c", cn).CombinedOutput()
	if err == nil && len(out) > 0 {
		return true, nil
	}
	// 2/3. 指定钥匙串兜底
	keychains := []string{
		expandHome(loginKeychain),            // ~/Library/Keychains/login.keychain-db
		"/Library/Keychains/System.keychain", // 系统钥匙串
	}
	for _, kc := range keychains {
		out, err := exec.Command("security", "find-certificate", "-c", cn, kc).CombinedOutput()
		if err == nil && len(out) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// expandHome 展开路径中的 ~/ 前缀为实际的家目录路径。
func expandHome(p string) string {
	home, _ := exec.Command("sh", "-c", "echo $HOME").Output()
	return strings.Replace(p, "~/", strings.TrimSpace(string(home))+string(filepath.Separator), 1)
}
