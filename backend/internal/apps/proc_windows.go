package apps

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinProc — процесс Windows: родитель и полный путь к исполняемому файлу.
type WinProc struct {
	PID, PPID uint32
	// Path — полный путь; пусто, если процесс закрыт для чтения (системный).
	Path string
	// Session — сеанс входа; 0 — службы.
	Session uint32
}

// Processes — снимок всех процессов.
func Processes() map[uint32]WinProc {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	out := make(map[uint32]WinProc)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		p := WinProc{PID: e.ProcessID, PPID: e.ParentProcessID, Path: ImagePath(e.ProcessID)}
		windows.ProcessIdToSessionId(e.ProcessID, &p.Session)
		out[p.PID] = p
	}
	return out
}

// ImagePath — полный путь к исполняемому файлу процесса.
func ImagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}
