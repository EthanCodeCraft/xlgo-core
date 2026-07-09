package database

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/EthanCodeCraft/xlgo-core/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// ensureMySQLTLSRegistered 按 cfg.Database 的 TLS 配置注册命名 TLS 配置到 go-sql-driver/mysql（M-config-2）。
//
// go-sql-driver/mysql v1.7.0 的 tls DSN 参数语义：
//   - tls=true：内置安全（&tls.Config{}，系统根 CA + ServerName 自动取自 host + 证书校验），无需注册。
//   - tls=<name>：引用经 RegisterTLSConfig 注册的命名配置，用于私有 CA/自签证书。
//
// 本函数仅在「TLS=true 且 TLSRootCA 非空」时注册 config.MySQLTLSConfigName 命名配置：
// 加载 TLSRootCA 的 PEM 到 RootCAs，ServerName 取自 Host，MinVersion=TLS1.2。其余情况（TLS 未启用、
// 或启用但用内置 tls=true）直接返回 nil。
//
// 非 MySQL 驱动（如 postgres，用 SSLMode）跳过。失败返回错误，由 initDB fail-fast，
// 绝不静默回退明文连接（生产 DB 流量明文是安全风险）。
//
// 注意：RegisterTLSConfig 是驱动级全局状态——一个名字对应唯一 *tls.Config（含唯一 ServerName），
// 同名重复注册会覆盖前者且不报错，故无法用「同名」同时匹配多个不同 host。
//
// 覆盖范围（注册配置的 ServerName 固定取自主库 Host）：
//   - 单 host 集群：replica DSN 的 host 与主库 Host 相同（同机不同端口，或经同一 LB/主机名暴露）——
//     master 与 replica DSN 均用 tls=MySQLTLSConfigName，ServerName 一致，握手通过。
//   - 多 host replicas + 私有 CA：replica DSN 的 host 与主库 Host 不同时，本注册的 ServerName（主库
//     host）与 replica 证书 SAN 不匹配，握手失败。此场景须用户为每个 replica host 注册**不同名**的
//     TLS 配置（各自 ServerName=该 replica host）并自行构造对应 replica DSN（tls=<该名>）；框架
//     MySQLDSN() 硬编码 tls=MySQLTLSConfigName，不能用于这些 replica DSN。切勿对 MySQLTLSConfigName
//     同名重复注册——会覆盖主库注册、导致主库握手失败。
//
// replica 路径（InitDBWithReplicas 的 replicaDSNs）由调用方传原始 DSN，不经本函数注册。
// 多集群不同 CA 亦属已知多 App 限制，需用户按上述方式自行注册。
func ensureMySQLTLSRegistered(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	db := &cfg.Database
	if !db.TLS || strings.TrimSpace(db.TLSRootCA) == "" {
		return nil
	}
	// 仅 MySQL 走注册路径；postgres 用 SSLMode，其他驱动自管 TLS。空 driver 默认 MySQL。
	drv := normalizeDriver(db.Driver)
	if drv != "" && drv != DriverMySQL {
		return nil
	}

	pem, err := os.ReadFile(db.TLSRootCA)
	if err != nil {
		return fmt.Errorf("读取 MySQL TLS CA 文件失败 %q: %w", db.TLSRootCA, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("解析 MySQL TLS CA 文件失败 %q: 非 PEM 格式或无有效证书", db.TLSRootCA)
	}
	tlsCfg := &tls.Config{
		RootCAs:    pool,
		ServerName: db.Host,
		MinVersion: tls.VersionTLS12, // 禁用 TLS1.0/1.1，符合安全基线
	}
	if err := mysqldriver.RegisterTLSConfig(config.MySQLTLSConfigName, tlsCfg); err != nil {
		return fmt.Errorf("注册 MySQL TLS 配置 %q 失败: %w", config.MySQLTLSConfigName, err)
	}
	return nil
}
