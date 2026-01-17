package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"otun-node-agent/internal/client"
)

// CertManager 证书管理器
type CertManager struct {
	dataDir   string
	certPath  string
	keyPath   string
	chainPath string
}

// NewCertManager 创建证书管理器
func NewCertManager(dataDir string) *CertManager {
	certDir := filepath.Join(dataDir, "certs")
	os.MkdirAll(certDir, 0700) // 证书目录需要严格权限

	return &CertManager{
		dataDir:   dataDir,
		certPath:  filepath.Join(certDir, "cert.pem"),
		keyPath:   filepath.Join(certDir, "key.pem"),
		chainPath: filepath.Join(certDir, "chain.pem"),
	}
}

// GetCertPath 获取证书文件路径
func (m *CertManager) GetCertPath() string {
	return m.certPath
}

// GetKeyPath 获取私钥文件路径
func (m *CertManager) GetKeyPath() string {
	return m.keyPath
}

// HasValidCert 检查是否有有效的证书
func (m *CertManager) HasValidCert() bool {
	// 简单检查文件是否存在
	_, certErr := os.Stat(m.certPath)
	_, keyErr := os.Stat(m.keyPath)
	return certErr == nil && keyErr == nil
}

// SaveCert 保存证书
func (m *CertManager) SaveCert(cert *client.CertResponse) error {
	// 写入证书
	if err := os.WriteFile(m.certPath, []byte(cert.Cert), 0644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}

	// 写入私钥 (严格权限)
	if err := os.WriteFile(m.keyPath, []byte(cert.Key), 0600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}

	// 写入证书链 (如果有)
	if cert.Chain != "" {
		if err := os.WriteFile(m.chainPath, []byte(cert.Chain), 0644); err != nil {
			return fmt.Errorf("write chain: %w", err)
		}
	}

	log.Printf("[CertManager] Certificate saved, expires at: %s", cert.ExpiresAt.Format("2006-01-02"))
	return nil
}

// FetchAndSaveCert 从 TLS 服务获取并保存证书
func (m *CertManager) FetchAndSaveCert(tlsClient *client.TLSClient, domain string) error {
	log.Printf("[CertManager] Fetching certificate for domain: %s", domain)

	// 尝试确保证书存在（不存在则申请）
	cert, err := tlsClient.EnsureCertificate(domain)
	if err != nil {
		return fmt.Errorf("ensure certificate: %w", err)
	}

	return m.SaveCert(cert)
}
