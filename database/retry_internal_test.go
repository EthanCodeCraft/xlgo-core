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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientDBError(tt.err); got != tt.want {
				t.Errorf("isTransientDBError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
