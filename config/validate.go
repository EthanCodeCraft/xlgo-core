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

	// Database：仅当配置了 driver 时校验（未启用 mysql 的项目可留空）
	if strings.TrimSpace(c.Database.Driver) != "" {
		if c.Database.Host == "" {
			problems = append(problems, "database.host 启用数据库后必填")
		}
		if c.Database.Name == "" {
			problems = append(problems, "database.name 启用数据库后必填")
		}
		if c.Database.Port <= 0 || c.Database.Port > 65535 {
			problems = append(problems, fmt.Sprintf("database.port 超出范围(1-65535): %d", c.Database.Port))
		}
	}

	// Redis：仅当配置了 host 时校验
	if strings.TrimSpace(c.Redis.Host) != "" {
		if c.Redis.Port <= 0 || c.Redis.Port > 65535 {
			problems = append(problems, fmt.Sprintf("redis.port 超出范围(1-65535): %d", c.Redis.Port))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("配置校验失败: %s", strings.Join(problems, "; "))
	}
	return nil
}

// validDuration 校验 Duration 非负（0 表示未配置/用默认，合法）。
func validDuration(d time.Duration) bool { return d >= 0 }
