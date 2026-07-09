package database

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// writeSelfSignedCAPEM 生成一个自签名 CA 证书并写入临时 PEM 文件，返回路径。
func writeSelfSignedCAPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "xlgo-test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	p := filepath.Join(os.TempDir(), "xlgo_tls_ca_test.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestEnsureMySQLTLSRegistered_NoOpCases 固化 M-config-2：非 MySQL / 未启用 TLS / 无 CA 时
// ensureMySQLTLSRegistered 为 no-op，不注册、不报错。
func TestEnsureMySQLTLSRegistered_NoOpCases(t *testing.T) {
	if err := ensureMySQLTLSRegistered(nil); err != nil {
		t.Errorf("nil cfg 应 no-op, got %v", err)
	}
	mysqlNoTLS := &config.Config{Database: config.DatabaseConfig{
		Driver: "mysql", Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
	}}
	if err := ensureMySQLTLSRegistered(mysqlNoTLS); err != nil {
		t.Errorf("TLS=false 应 no-op, got %v", err)
	}
	mysqlBuiltIn := &config.Config{Database: config.DatabaseConfig{
		Driver: "mysql", Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
		TLS: true, // 无 TLSRootCA，用内置 tls=true
	}}
	if err := ensureMySQLTLSRegistered(mysqlBuiltIn); err != nil {
		t.Errorf("TLS=true 无 CA 应 no-op（内置 tls=true）, got %v", err)
	}
	// postgres 用 SSLMode，不走 MySQL TLS 注册
	pg := &config.Config{Database: config.DatabaseConfig{
		Driver: config.DriverPostgres, Host: "h", Port: 5432, User: "u", Password: "p", Name: "n",
		TLS: true, TLSRootCA: "/path/ca.pem",
	}}
	if err := ensureMySQLTLSRegistered(pg); err != nil {
		t.Errorf("postgres 应跳过 MySQL TLS 注册, got %v", err)
	}
	// 空 driver（默认 MySQL）+ 无 TLS：no-op
	emptyDrv := &config.Config{Database: config.DatabaseConfig{
		Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
	}}
	if err := ensureMySQLTLSRegistered(emptyDrv); err != nil {
		t.Errorf("空 driver 无 TLS 应 no-op, got %v", err)
	}
}

// TestEnsureMySQLTLSRegistered_BadCA 固化 M-config-2：CA 文件不可读或非 PEM 时 fail-fast，
// 绝不静默回退明文连接。
func TestEnsureMySQLTLSRegistered_BadCA(t *testing.T) {
	missing := &config.Config{Database: config.DatabaseConfig{
		Driver: "mysql", Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
		TLS: true, TLSRootCA: "/no/such/ca.pem",
	}}
	if err := ensureMySQLTLSRegistered(missing); err == nil || !strings.Contains(err.Error(), "读取") {
		t.Fatalf("CA 文件不存在应报读取错误, got: %v", err)
	}

	nonPEM := filepath.Join(os.TempDir(), "xlgo_tls_nonpem.pem")
	if err := os.WriteFile(nonPEM, []byte("this is not a pem file"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bad := &config.Config{Database: config.DatabaseConfig{
		Driver: "mysql", Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
		TLS: true, TLSRootCA: nonPEM,
	}}
	if err := ensureMySQLTLSRegistered(bad); err == nil || !strings.Contains(err.Error(), "解析") {
		t.Fatalf("非 PEM 文件应报解析错误, got: %v", err)
	}
}

// TestEnsureMySQLTLSRegistered_ResolvesViaParseDSN 固化 M-config-2 端到端：
// 注册前 ParseDSN(tls=xlgo-mysql) 报 unknown config name；注册后成功解析且 TLS 非 nil。
// 利用 go-sql-driver/mysql 的 TLS 解析发生在 ParseDSN 的 normalize() 阶段，无需真实 DB 连接。
func TestEnsureMySQLTLSRegistered_ResolvesViaParseDSN(t *testing.T) {
	caPath := writeSelfSignedCAPEM(t)
	defer os.Remove(caPath)

	// 清理同名残留注册，保证「注册前」断言不被前序测试污染
	mysqldriver.DeregisterTLSConfig(config.MySQLTLSConfigName)
	defer mysqldriver.DeregisterTLSConfig(config.MySQLTLSConfigName)

	cfg := &config.Config{Database: config.DatabaseConfig{
		Driver: "mysql", Host: "127.0.0.1", Port: 3306, User: "u", Password: "p", Name: "n",
		TLS: true, TLSRootCA: caPath,
	}}
	dsn := cfg.Database.DSN()
	if !strings.Contains(dsn, "tls="+config.MySQLTLSConfigName) {
		t.Fatalf("DSN 应含 tls=%s: %s", config.MySQLTLSConfigName, dsn)
	}

	// 注册前：ParseDSN 应报 unknown config name
	if _, err := mysqldriver.ParseDSN(dsn); err == nil {
		t.Fatal("注册前 ParseDSN 不应成功（未知 TLS 配置名）")
	} else if !strings.Contains(err.Error(), "unknown config name") {
		t.Logf("注册前 ParseDSN 错误（预期未知配置名）: %v", err)
	}

	// 注册后：ParseDSN 成功，TLS 已解析为非 nil
	if err := ensureMySQLTLSRegistered(cfg); err != nil {
		t.Fatalf("ensureMySQLTLSRegistered: %v", err)
	}
	mc, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("注册后 ParseDSN 应成功, got: %v", err)
	}
	if mc.TLS == nil {
		t.Error("注册后 ParseDSN 的 TLS 配置应为非 nil（CA 已注册）")
	}
	if mc.TLS.ServerName != "127.0.0.1" {
		t.Errorf("TLS ServerName = %q, want 127.0.0.1", mc.TLS.ServerName)
	}
}
