package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// privateDirSDDL — доступ только системе и администраторам, без
// наследования прав от родителя.
//
// Без этого каталог в ProgramData унаследовал бы право чтения для всех
// пользователей машины, а в нём лежат приватные ключи всех подключений.
const privateDirSDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// DefaultDir — каталог состояния службы: %ProgramData%\awg-client.
func DefaultDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "awg-client")
}

// EnsurePrivateDir создаёт каталог, доступный только системе и
// администраторам. Права выставляются и на уже существующий каталог.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	sd, err := windows.SecurityDescriptorFromString(privateDirSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}

	return windows.SetNamedSecurityInfo(
		filepath.Clean(dir),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}
