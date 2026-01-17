package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TLSClient TLS 服务客户端
type TLSClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewTLSClient 创建 TLS 客户端
func NewTLSClient(baseURL, apiKey string) *TLSClient {
	return &TLSClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second, // 证书申请可能需要较长时间
		},
	}
}

// CertResponse 证书响应
type CertResponse struct {
	Domain    string    `json:"domain"`
	Cert      string    `json:"cert"`       // PEM 格式证书
	Key       string    `json:"key"`        // PEM 格式私钥
	Chain     string    `json:"chain"`      // 证书链
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetCertificate 获取证书（仅当已存在时）
func (c *TLSClient) GetCertificate(domain string) (*CertResponse, error) {
	url := fmt.Sprintf("%s/api/certs/%s", c.baseURL, domain)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("certificate not found for domain: %s", domain)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var certResp CertResponse
	if err := json.NewDecoder(resp.Body).Decode(&certResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &certResp, nil
}

// EnsureCertificate 确保证书存在（不存在则申请）
func (c *TLSClient) EnsureCertificate(domain string) (*CertResponse, error) {
	url := fmt.Sprintf("%s/api/certs/%s/ensure", c.baseURL, domain)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var certResp CertResponse
	if err := json.NewDecoder(resp.Body).Decode(&certResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &certResp, nil
}
