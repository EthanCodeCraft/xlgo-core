package database

import (
	"errors"
	"testing"
)

func TestIsTransientDBError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"access denied", errors.New("Error 1045: Access denied for user 'root'@'localhost'"), false},
		{"auth plugin", errors.New("authentication plugin 'caching_sha2_password' cannot be loaded"), false},
		{"unknown database", errors.New("Error 1049: Unknown database 'foo'"), false},
		{"invalid DSN", errors.New("invalid DSN: missing the slash separating the database name"), false},
		{"unknown driver", errors.New("sql: unknown driver \"foobar\" (forgotten import?)"), false},
		{"connection refused (transient)", errors.New("dial tcp 127.0.0.1:3306: connect: connection refused"), true},
		{"i/o timeout (transient)", errors.New("dial tcp 10.0.0.1:3306: i/o timeout"), true},
		{"empty msg", errors.New(""), true},
		// P1 #12：PG 目标库不存在应视为非瞬态（不重试）。
		{"pg database not exist", errors.New(`FATAL: database "app" does not exist (SQLSTATE 3D000)`), false},
		{"pg password auth failed", errors.New("FATAL: password authentication failed for user \"app\""), false},
		// P1 #12 回归核心：含 "database" 但是瞬态的错误必须仍被判为瞬态（可重试），
		// 修复前独立 "database" 子串会把它误判为非瞬态而放弃重试。
		{"pg starting up (transient)", errors.New("FATAL: the database system is starting up (SQLSTATE 57P03)"), true},
		{"pg in recovery (transient)", errors.New("FATAL: the database system is in recovery mode"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientDBError(tt.err); got != tt.want {
				t.Errorf("isTransientDBError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
