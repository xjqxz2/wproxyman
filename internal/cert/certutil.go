// Package cert 证书相关工具函数。
// certutil.go 提供从 PEM 文件中读取 X.509 证书并提取序列号、通用名称等信息的辅助函数。
// 这些函数被平台特定的信任存储实现（trust_*.go）使用。
package cert

import (
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
)

// readCertFromFile 从指定路径解析第一个 PEM 编码的证书块。
// 返回解析后的 X.509 证书，如果文件不存在或格式无效则返回错误。
func readCertFromFile(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("no certificate found in %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

// serialFromFile 读取证书序列号，返回以空格分隔的大写十六进制字符串（Windows certutil 格式）。
// 例如：序列号 0x01AB → "01 AB"
func serialFromFile(path string) (string, error) {
	cert, err := readCertFromFile(path)
	if err != nil {
		return "", err
	}
	raw := strings.ToUpper(hex.EncodeToString(cert.SerialNumber.Bytes()))
	// 将十六进制字符串按两个字符一组分割，用空格连接
	parts := make([]string, 0, len(raw)/2)
	for i := 0; i+2 <= len(raw); i += 2 {
		parts = append(parts, raw[i:i+2])
	}
	return strings.Join(parts, " "), nil
}

// commonNameFromFile 读取证书的通用名称（CN，Common Name）。
func commonNameFromFile(path string) (string, error) {
	cert, err := readCertFromFile(path)
	if err != nil {
		return "", err
	}
	return cert.Subject.CommonName, nil
}
