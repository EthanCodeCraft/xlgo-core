package config_test

import (
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
)

func validBase() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Port: 8080},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validBase().Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateServerPort(t *testing.T) {
	c := validBase()
	c.Server.Port = 99999
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "server.port") {
		t.Fatalf("expected server.port error, got %v", err)
	}
}

func TestValidateJWTSecretTooShort(t *testing.T) {
	c := validBase()
	c.JWT.Secret = "short"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "jwt.secret") {
		t.Fatalf("expected jwt.secret error, got %v", err)
	}
}

func TestValidateJWTSecretAbsentSkipped(t *testing.T) {
	c := validBase()
	// secret 为空时不校验（未启用 jwt）
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDatabaseMissingHost(t *testing.T) {
	c := validBase()
	c.Database.Driver = "mysql"
	c.Database.Port = 3306
	c.Database.Name = "db"
	// Host 留空
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "database.host") {
		t.Fatalf("expected database.host error, got %v", err)
	}
}

func TestValidateTLSMissingCert(t *testing.T) {
	c := validBase()
	c.Server.TLS.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "cert_file") {
		t.Fatalf("expected tls cert_file error, got %v", err)
	}
}
