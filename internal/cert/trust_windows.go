//go:build windows

// Package cert Windows 平台的信任存储实现。
// trust_windows.go 使用 Windows CryptoAPI（crypt32.dll）将 CA 证书安装到
// 当前用户的"受信任的根证书颁发机构"（Root）存储中。
//
// 设计要点：
//   - 主要路径：通过 golang.org/x/sys/windows 绑定直接调用 crypt32 API，
//     无子进程、无控制台窗口闪烁
//   - 备用路径：当 CryptoAPI 根存储写入操作挂起时（某些系统上受信任服务检查阻塞），
//     回退到 certutil.exe 命令，并设置超时以确保应用不会卡死
//   - Install 使用 goroutine + 超时模式，主路径 12 秒超时
//   - Remove 通过序列号匹配删除证书，此操作不会触发信任服务检查挂起
package cert

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformTrustStore 是 Windows 平台的信任存储实现（空结构体，无状态）。
type platformTrustStore struct{}

// crypt32 系统存储标志常量。
const (
	x509ASNEncoding            = 0x00000001 // X509_ASN_ENCODING：ASN.1 编码格式
	certStoreAddReplaceExisting = 3          // CERT_STORE_ADD_REPLACE_EXISTING：替换已有的同名证书
)

// openRootStore 打开当前用户的 Root（受信任根证书）存储，可读写。
// 使用 CertOpenSystemStore API（参数 0 表示当前用户）。
func openRootStore() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("Root")
	if err != nil {
		return 0, err
	}
	store, err := windows.CertOpenSystemStore(0, name)
	if err != nil {
		return 0, err
	}
	return store, nil
}

// sha1Sum 计算字节切片的 SHA-1 哈希值，返回 20 字节摘要。
func sha1Sum(b []byte) []byte {
	s := sha1.Sum(b)
	return s[:]
}

// certInRootStore 检查 DER 编码的证书（通过 SHA-1 指纹匹配）是否存在于当前用户的
// Root 存储中。使用官方 x/sys/windows crypt32 绑定——无子进程、无控制台闪烁、快速。
func certInRootStore(der []byte) bool {
	// 计算目标证书的 SHA-1 指纹（大写十六进制）
	want := strings.ToUpper(hex.EncodeToString(sha1Sum(der)))
	store, err := openRootStore()
	if err != nil {
		return false
	}
	defer func() { _ = windows.CertCloseStore(store, 0) }()

	// 遍历 Root 存储中的所有证书，逐一比对 SHA-1 指纹
	var prev *windows.CertContext
	for {
		ctx, err := windows.CertEnumCertificatesInStore(store, prev)
		if err != nil {
			break // 遍历结束或出错
		}
		prev = ctx
		if ctx.Length == 0 || ctx.EncodedCert == nil {
			continue
		}
		// 使用 unsafe.Slice 从原始内存构造切片，然后计算 SHA-1 指纹比对
		raw := unsafe.Slice(ctx.EncodedCert, int(ctx.Length))
		if strings.ToUpper(hex.EncodeToString(sha1Sum(raw))) == want {
			return true
		}
	}
	return false
}

// addCertToRootStore 将 DER 编码的证书添加到当前用户的 Root 存储。
// 使用 CertCreateCertificateContext 创建证书上下文，
// 然后用 CertAddCertificateContextToStore 将其添加到存储。
func addCertToRootStore(der []byte) error {
	if len(der) == 0 {
		return fmt.Errorf("empty certificate data")
	}
	// 创建证书上下文（CryptoAPI 结构）
	ctx, err := windows.CertCreateCertificateContext(x509ASNEncoding, &der[0], uint32(len(der)))
	if err != nil {
		return fmt.Errorf("CertCreateCertificateContext: %w", err)
	}
	defer windows.CertFreeCertificateContext(ctx) // 确保释放证书上下文

	store, err := openRootStore()
	if err != nil {
		return err
	}
	defer func() { _ = windows.CertCloseStore(store, 0) }()

	// 添加到存储，已存在则替换
	return windows.CertAddCertificateContextToStore(store, ctx, certStoreAddReplaceExisting, nil)
}

// --- certutil 备用方案（无控制台窗口弹出） ---

// silentCmd 以无控制台窗口模式运行控制台程序。
// 设置 CREATE_NO_WINDOW 标志，避免弹出 cmd 窗口。
func silentCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

// installTimeout 是安装/删除操作的总超时时间。
const installTimeout = 12 * time.Second

// Install 通过 crypt32 将 CA 添加到当前用户的 Root 存储中。
// 在某些系统上，CryptoAPI 根存储写入可能挂起（受信任服务检查阻塞），
// 此时会回退到带超时的 certutil 方案，确保应用不会卡死。
func (platformTrustStore) Install(caPath string) error {
	c, err := readCertFromFile(caPath)
	if err != nil {
		return err
	}
	// 在新的 goroutine 中尝试 CryptoAPI 方式写入
	done := make(chan error, 1)
	go func() {
		done <- addCertToRootStore(c.Raw)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(installTimeout):
		// CryptoAPI 根存储写入挂起了（此机器上受信任服务检查阻塞）
		// 回退到 certutil，同样设置超时
		ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
		defer cancel()
		_, cerr := silentCmd(ctx, "certutil", "-addstore", "-user", "Root", caPath).CombinedOutput()
		if cerr != nil {
			return fmt.Errorf("the system trust service is blocking certificate install; open %s and install manually (certmgr → Trusted Root → Import)", caPath)
		}
		return nil
	}
}

// Remove 通过序列号匹配从当前用户的 Root 存储中删除 CA 证书。
// 删除操作通过 certutil 执行（不会触发信任服务检查挂起问题）。
// 操作有超时限制，确保应用不会卡死。
func (platformTrustStore) Remove(caPath string) error {
	// 通过序列号删除（此操作不会触发信任检查挂起）
	// 操作限制超时，确保应用不会卡死
	serial, err := serialFromFile(caPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	out, err := silentCmd(ctx, "certutil", "-delstore", "-user", "Root", serial).CombinedOutput()
	if err != nil {
		// 如果证书已不存在（带 "not found" 关键字），视为删除成功
		if strings.Contains(strings.ToLower(string(out)), "not found") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil
		}
		return fmt.Errorf("certutil delstore failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsInstalled 检查 CA 证书是否已在当前用户的 Root 存储中（通过 SHA-1 指纹匹配）。
func (platformTrustStore) IsInstalled(caPath string) (bool, error) {
	c, err := readCertFromFile(caPath)
	if err != nil {
		return false, err
	}
	return certInRootStore(c.Raw), nil
}
