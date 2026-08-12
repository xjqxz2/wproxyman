// Package cert 实现了一个本地证书颁发机构（CA），用于 TLS 中间人（MITM）拦截。
// 该包负责：
//   1. 创建/加载自签名的 CA 根证书（RSA 2048 位）
//   2. 为每个被拦截的主机名动态签发叶子证书并缓存
//   3. 将 CA 证书安装到各平台（Windows/macOS/Linux）的受信任根证书存储中
//
// 设计要点：
//   - CA 证书和密钥持久化到磁盘（ca.crt / ca.key），重启后复用，避免重复信任授权
//   - 叶子证书按主机名缓存（hostCerts map），使用双重检查锁定避免惊群效应
//   - 证书有效期：CA 默认 10 年，叶子证书约 13 个月
//   - 签名算法：RSA 2048 + SHA-256
//
// 与其他模块的关系：
//   - proxy 模块在 TLS 握手时调用 CA.SignForHost() 获取动态证书
//   - trust_*.go 文件（按平台编译）负责将 CA 安装到系统信任存储
package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultOrg 是生成的证书中内嵌的组织名称。
const DefaultOrg = "WProxyman"

// CA 是一个持久化到磁盘的证书颁发机构。
// 它持有自签名的根证书/私钥，并为拦截的主机名动态签发叶子证书。
type CA struct {
	mu          sync.RWMutex          // 保护并发访问的读写锁
	Cert        *x509.Certificate     // 解析后的 CA 根证书
	Key         *rsa.PrivateKey       // CA 私钥（RSA）
	certPEM     []byte                // PEM 编码的证书（用于磁盘存储和传输）
	keyPEM      []byte                // PEM 编码的私钥
	hostCerts   map[string]*tls.Certificate // 主机名 → 叶子证书缓存
	certDir     string                // 证书存储目录
	caFile      string                // CA 证书文件路径
	keyFile     string                // CA 私钥文件路径
	orgName     string                // 证书组织名称
	notBefore   time.Time             // CA 生效时间
	notAfter    time.Time             // CA 过期时间
}

// Options 配置 CA 的创建参数。
type Options struct {
	Org       string // 组织名称（为空则使用 DefaultOrg）
	ValidDays int    // CA 有效期（天），默认 3650（10 年）
	CertFile  string // 显式指定 CA 证书的存储路径
	KeyFile   string // 显式指定 CA 私钥的存储路径
}

// LoadOrCreate 从磁盘加载已有的 CA，如果不存在则创建新的。
// dir 参数指定 ca.crt 和 ca.key 的持久化目录。
// 返回加载或新创建的 CA 实例，以及可能的错误。
func LoadOrCreate(dir string, opts Options) (*CA, error) {
	// 应用默认值
	if opts.Org == "" {
		opts.Org = DefaultOrg
	}
	if opts.ValidDays == 0 {
		opts.ValidDays = 3650 // 默认 10 年有效期
	}
	caFile := opts.CertFile
	keyFile := opts.KeyFile
	if caFile == "" {
		caFile = filepath.Join(dir, "ca.crt")
	}
	if keyFile == "" {
		keyFile = filepath.Join(dir, "ca.key")
	}

	// 确保证书目录存在
	if err := os.MkdirAll(filepath.Dir(caFile), 0o755); err != nil {
		return nil, err
	}

	ca := &CA{
		hostCerts: make(map[string]*tls.Certificate),
		certDir:   dir,
		caFile:    caFile,
		keyFile:   keyFile,
		orgName:   opts.Org,
		notBefore: time.Now().Add(-24 * time.Hour), // 从 24 小时前开始生效，避免时钟偏差
		notAfter:  time.Now().AddDate(0, 0, opts.ValidDays),
	}

	// 尝试从磁盘加载已有的 CA 证书和私钥
	certPEM, errCert := os.ReadFile(caFile)
	keyPEM, errKey := os.ReadFile(keyFile)
	if errCert == nil && errKey == nil {
		if parsed, err := ca.parse(certPEM, keyPEM); err == nil {
			ca.certPEM = certPEM
			ca.keyPEM = keyPEM
			ca.Cert = parsed.cert
			ca.Key = parsed.key
			return ca, nil
		}
		// 证书损坏或过期 → 将在下面重新生成
	}

	// 生成新的 CA 证书并持久化
	if err := ca.generate(opts.ValidDays); err != nil {
		return nil, err
	}
	if err := ca.save(); err != nil {
		return nil, err
	}
	return ca, nil
}

// parsedCA 是解析后的 CA 证书和私钥对。
type parsedCA struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
}

// parse 解析 PEM 编码的 CA 证书和私钥，并验证其有效性。
// 会检查证书是否在有效期内、是否为 CA 证书。
func (c *CA) parse(certPEM, keyPEM []byte) (*parsedCA, error) {
	// 解码证书 PEM
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	// 检查证书是否过期
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, fmt.Errorf("CA certificate is outside its validity window")
	}
	// 确保证书确实是 CA 证书（具有证书签发能力）
	if !cert.IsCA {
		return nil, fmt.Errorf("certificate is not a CA")
	}
	// 解码私钥 PEM
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("invalid CA key PEM")
	}
	// 先尝试 PKCS#1 格式的 RSA 私钥
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		// 如果 PKCS#1 解析失败，尝试 PKCS#8 格式
		parsed, err2 := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("invalid CA key: %v", err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("CA key is not RSA")
		}
		key = rsaKey
	}
	return &parsedCA{cert: cert, key: key}, nil
}

// generate 生成新的自签名 CA 根证书和 RSA 私钥。
// 使用 RSA 2048 位密钥，有效期由 validDays 指定。
func (c *CA) generate(validDays int) error {
	// 生成 2048 位 RSA 私钥
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	// 生成 128 位随机序列号
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	// 构建 CA 证书模板
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: c.orgName + " CA", Organization: []string{c.orgName}},
		NotBefore:             c.notBefore,
		NotAfter:              c.notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1, // 允许签发一级子证书（叶子证书）
	}
	// 自签名：签发者和主体相同
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	c.Cert = cert
	c.Key = key
	c.certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	c.keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return nil
}

// save 将 CA 证书和私钥写入磁盘。
// 证书权限 0644（可读），私钥权限 0600（仅所有者可读写）。
func (c *CA) save() error {
	if err := os.WriteFile(c.caFile, c.certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(c.keyFile, c.keyPEM, 0o600); err != nil {
		return err
	}
	return nil
}

// CertPEM 返回 PEM 编码的 CA 证书（线程安全）。
func (c *CA) CertPEM() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.certPEM
}

// KeyPEM 返回 PEM 编码的 CA 私钥（线程安全）。
func (c *CA) KeyPEM() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.keyPEM
}

// CertFile 返回 CA 证书的磁盘路径。
func (c *CA) CertFile() string { return c.caFile }

// KeyFile 返回 CA 私钥的磁盘路径。
func (c *CA) KeyFile() string { return c.keyFile }

// IssuerName 返回 CA 证书的通用名称（CN）。
func (c *CA) IssuerName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Cert == nil {
		return ""
	}
	return c.Cert.Subject.CommonName
}

// SignForHost 为给定的主机名返回已缓存的 TLS 证书。
// 如果缓存中不存在，则动态生成新的叶子证书并缓存。
// 证书对该主机名有效，同时包含通配符子域名（如 *.example.com）。
// 使用双重检查锁定模式：先在读锁下查找缓存，未命中时升级为写锁并重新检查。
func (c *CA) SignForHost(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, fmt.Errorf("empty host")
	}

	// 快速路径：在读锁下检查缓存（大多数请求命中此路径）
	c.mu.RLock()
	if cached, ok := c.hostCerts[host]; ok {
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	// 慢路径：升级为写锁，使用每个主机名级别的锁避免惊群效应
	// 双重检查：获取写锁后再次检查缓存（其他 goroutine 可能已生成）
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.hostCerts[host]; ok {
		return cached, nil
	}

	cert, err := c.sign(host)
	if err != nil {
		return nil, err
	}
	c.hostCerts[host] = cert
	return cert, nil
}

// sign 为指定主机名生成叶子证书，由 CA 根证书签名。
// 证书包含精确主机名和通配符子域名（对非本地主机名和至少有一级子域的主机）。
// 叶子证书有效期约 13 个月（397 天）。
func (c *CA) sign(host string) (*tls.Certificate, error) {
	// 为叶子证书生成新的 RSA 2048 位私钥
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	// 构建 DNS 名称列表
	dnsNames := []string{host}
	if ip := net.ParseIP(host); ip == nil {
		// 不是 IP 地址 → 添加通配符子域名
		// 对 TLD 级别的主机名（如 localhost）不添加通配符
		if strings.Count(host, ".") >= 1 && !isLocalHostname(host) {
			dnsNames = append(dnsNames, "*."+host)
		}
	}

	// 构建叶子证书模板
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{c.orgName}},
		NotBefore:    time.Now().Add(-1 * time.Hour),  // 从前 1 小时开始生效，容忍时钟偏差
		NotAfter:     time.Now().AddDate(0, 0, 397),   // 约 13 个月后过期
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, // 用于 TLS 服务器认证
		DNSNames:     dnsNames,
		IPAddresses:  ipList(host),
	}
	// 使用 CA 私钥签发叶子证书
	der, err := x509.CreateCertificate(rand.Reader, template, c.Cert, &leafKey.PublicKey, c.Key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	// 返回包含完整证书链的 TLS 证书（叶子 + CA）
	return &tls.Certificate{
		Certificate: [][]byte{der, c.Cert.Raw}, // 证书链：叶子在前，CA 在后
		PrivateKey:  leafKey,
		Leaf:        leaf,
	}, nil
}

// ipList 将主机字符串解析为 IP 列表。如果不是 IP 则返回 nil。
func ipList(host string) []net.IP {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	return []net.IP{ip}
}

// isLocalHostname 判断主机名是否为本地/特殊主机名（不应添加通配符）。
func isLocalHostname(host string) bool {
	switch host {
	case "localhost", "localhost.localdomain", "local", "broadcasthost":
		return true
	}
	return false
}

// randomSerial 生成 128 位的随机证书序列号。
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128) // 2^128 上限
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, err
	}
	return n, nil
}
