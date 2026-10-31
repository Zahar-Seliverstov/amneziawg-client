//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

// DefaultDir — каталог состояния службы. В нём лежат приватные ключи всех
// подключений, поэтому он закрыт от всех, кроме root.
func DefaultDir() string {
	return "/var/lib/awg-client"
}

// EnsurePrivateDir создаёт каталог, доступный только владельцу.
//
// Права выставляются и на уже существующий каталог: его мог создать кто-то
// другой и оставить открытым — а лежат в нём ключи.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(filepath.Clean(dir), 0700)
}
