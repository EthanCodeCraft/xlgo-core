package config

import (
	"fmt"
	"strings"
	"time"
)

// Validate 校验配置完整性与取值合法性（#16）。
// 在 Manager.Load 解析后自动调用，把"运行时第一次请求才暴露"的配置错误
// 提前到进程启动期。返回的 error 描述具体字段，便于定位。
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("配置为空")
	}
	var problems []string

	// Server
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		problems = append(problems, fmt.Sprintf("server.port 超出范围(0-65535): %d", c.Server.Port))
	}
	if c.Server.TLS.Enabled {
		if strings.TrimSpace(c.Server.TLS.CertFile) == "" || strings.TrimSpace(c.Server.TLS.KeyFile) == "" {
			problems = append(problems, "server.tls 启用后必须同时配置 cert_file 与 key_file")
		}
	}
	if !validDuration(c.Server.ReadTimeout) || !validDuration(c.Server.WriteTimeout) ||
		!validDuration(c.Server.IdleTimeout) || !validDuration(c.Server.ShutdownTimeout) {
		problems = append(problems, "server 的 timeout 配置不能为负值")
	}

	// JWT：仅当配置了 secret 时校验（未启用 jwt 的项目可留空）
	if c.JWT.Secret != "" {
		if len(c.JWT.Secret) < 32 {
			problems = append(problems, fmt.Sprintf("jwt.secret 长度不足 32 字节（当前 %d），HMAC 密钥过短不安全", len(c.JWT.Secret)))
		}
		if c.JWT.Expire < 0 || c.JWT.RefreshExpire < 0 {
			problems = append(problems, "jwt.expire / jwt.refresh_expire 不能为负值")
		}
	}

	// Database：出现任一数据库字段时视为启用；driver 为空按 MySQL 兼容处理。
	if c.Database.isConfigured() {
		driver := strings.TrimSpace(c.Database.Driver)
		if driver != "" {
			if _, ok := LookupDSNBuilder(driver); !ok {
				problems = append(problems, fmt.Sprintf("database.driver 未注册: %s", driver))
			}
		}
		if strings.TrimSpace(c.Database.CustomDSN) == "" && strings.TrimSpace(c.Database.Host) == "" {
			problems = append(problems, "database.host 启用数据库后必填")
		}
		if strings.TrimSpace(c.Database.CustomDSN) == "" && strings.TrimSpace(c.Database.Name) == "" {
			problems = append(problems, "database.name 启用数据库后必填")
		}
		if strings.TrimSpace(c.Database.CustomDSN) == "" && (c.Database.Port <= 0 || c.Database.Port > 65535) {
			problems = append(problems, fmt.Sprintf("database.port 超出范围(1-65535): %d", c.Database.Port))
		}
		// L-config-3：连接池配置交叉校验。MaxOpenConns>0 时 MaxIdleConns 不应超过它，
		// 否则 database/sql 会按 MaxOpenConns 截断空闲连接，配置意图与实际不符。
		if c.Database.MaxOpenConns > 0 && c.Database.MaxIdleConns > c.Database.MaxOpenConns {
			problems = append(problems, fmt.Sprintf(
				"database.max_idle_conns(%d) 不应大于 max_open_conns(%d)", c.Database.MaxIdleConns, c.Database.MaxOpenConns))
		}
		// M-config-2：Postgres SSLMode 合法性（非空时校验，空由 PostgresDSN 默认 prefer）。
		if sslmode := strings.TrimSpace(c.Database.SSLMode); sslmode != "" {
			if !validPostgresSSLMode(sslmode) {
				problems = append(problems, fmt.Sprintf(
					"database.ssl_mode 非法: %s（允许: disable/allow/prefer/require/verify-ca/verify-full）", sslmode))
			}
		}
		// M-config-2：TLSRootCA 仅在 TLS=true 时生效，单独配置而无 tls:true 视为配置不一致。
		if strings.TrimSpace(c.Database.TLSRootCA) != "" && !c.Database.TLS {
			problems = append(problems, "database.tls_root_ca 需配合 tls: true 才生效")
		}
	}

	// Redis：仅当配置了 host 时校验
	if strings.TrimSpace(c.Redis.Host) != "" {
		if c.Redis.Port <= 0 || c.Redis.Port > 65535 {
			problems = append(problems, fmt.Sprintf("redis.port 超出范围(1-65535): %d", c.Redis.Port))
		}
	}

	// Trace：仅当启用时校验采样比例。ExporterType/Propagator 的枚举校验委托 trace.Init
	// （随 OTel 版本可能扩展，避免 config 与 trace 双源枚举漂移）。
	if c.Trace.Enabled {
		if c.Trace.SampleRatio < 0 || c.Trace.SampleRatio > 1 {
			problems = append(problems, fmt.Sprintf("trace.sample_ratio 超出范围(0.0-1.0): %v", c.Trace.SampleRatio))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("配置校验失败: %s", strings.Join(problems, "; "))
	}
	return nil
}

// validDuration 校验 Duration 非负（0 表示未配置/用默认，合法）。
func validDuration(d time.Duration) bool { return d >= 0 }

// validPostgresSSLMode 校验 PostgreSQL sslmode 取值（M-config-2）。
func validPostgresSSLMode(s string) bool {
	switch s {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return true
	}
	return false
}
