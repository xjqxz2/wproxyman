// Package cert 证书信任存储。
// trust.go 定义了 TrustStore 接口——将 CA 证书安装到/移除自操作系统信任存储的抽象。
// 各平台的具体实现通过 build tag 在 trust_windows.go、trust_darwin.go、trust_linux.go 中提供。
package cert

// TrustStore 定义了将 CA 证书安装到/移除自操作系统信任存储的操作。
// 不同操作系统（Windows/macOS/Linux）有各自的具体实现。
type TrustStore interface {
	// Install 将 CA 证书添加到用户信任存储中。
	// caPath 是 PEM 格式的 CA 证书文件路径。
	Install(caPath string) error
	// Remove 从用户信任存储中删除 CA 证书。
	// caPath 是 PEM 格式的 CA 证书文件路径。
	Remove(caPath string) error
	// IsInstalled 报告 CA 证书当前是否被系统信任。
	IsInstalled(caPath string) (bool, error)
}

// NewTrustStore 返回当前平台的 TrustStore 实现。
// 通过 platformTrustStore 结构体（在各平台的 trust_*.go 中定义）实现多态。
func NewTrustStore() TrustStore {
	return platformTrustStore{}
}
