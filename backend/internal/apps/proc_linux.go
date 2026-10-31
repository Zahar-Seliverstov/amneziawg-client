//go:build linux

package apps

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MinCmdlineMatch — с какой длины имя ищется и в командной строке: короткое
// («sh», «go») нашлось бы почти в любой.
const MinCmdlineMatch = 4

// Proc — то, по чему узнаётся процесс. Всё в нижнем регистре, кроме Cgroup.
type Proc struct {
	PID     int
	PPID    int
	Comm    string
	Exe     string
	Argv0   string
	Args    []string
	Cmdline string
	Cgroup  string
}

// ListPIDs — номера всех процессов.
func ListPIDs() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// ReadProc читает процесс. false — его уже нет или это поток ядра.
func ReadProc(pid int) (Proc, bool) {
	base := filepath.Join("/proc", strconv.Itoa(pid))

	raw, err := os.ReadFile(filepath.Join(base, "cmdline"))
	if err != nil || len(raw) == 0 {
		// Потоки ядра командной строки не имеют — их не трогаем.
		return Proc{}, false
	}

	p := Proc{PID: pid}
	for _, arg := range bytes.Split(bytes.TrimRight(raw, "\x00"), []byte{0}) {
		p.Args = append(p.Args, string(arg))
	}
	p.Argv0 = strings.ToLower(filepath.Base(p.Args[0]))
	p.Cmdline = strings.ToLower(strings.Join(p.Args, " "))

	if comm, err := os.ReadFile(filepath.Join(base, "comm")); err == nil {
		p.Comm = strings.ToLower(strings.TrimSpace(string(comm)))
	}
	if exe, err := os.Readlink(filepath.Join(base, "exe")); err == nil {
		p.Exe = strings.ToLower(filepath.Base(strings.TrimSuffix(exe, " (deleted)")))
	}

	cg, err := os.ReadFile(filepath.Join(base, "cgroup"))
	if err != nil {
		return Proc{}, false
	}
	p.Cgroup = ParseCgroupV2(string(cg))

	if stat, err := os.ReadFile(filepath.Join(base, "stat")); err == nil {
		p.PPID = ParsePPID(string(stat))
	}

	return p, true
}

// UserProcess — процесс запущен пользователем, а не системой. Правила для
// программ трогают только такие: системные службы живут в своих юнитах, и
// вынимать их оттуда по совпавшему слову нельзя.
func (p Proc) UserProcess() bool {
	return strings.HasPrefix(p.Cgroup, "/user.slice/")
}

// Match возвращает имя из набора, под которое подходит процесс, или пустую
// строку.
//
// Порядок — от точного к общему: имя процесса или исполняемого файла, затем
// приложение Flatpak по его идентификатору в имени cgroup, затем слово в
// командной строке. Последнее нужно ради программ на Java и Python: у
// Minecraft имя процесса — java, а слово minecraft есть только в аргументах.
func Match(p Proc, names []string) string {
	for _, name := range names {
		if p.Comm == name || p.Exe == name || p.Argv0 == name {
			return name
		}
	}
	scope := strings.ToLower(p.Cgroup)
	for _, name := range names {
		// Идентификатор Flatpak — com.example.App: внутри песочницы процесс
		// зовётся как угодно, но его cgroup — app-flatpak-<id>-N.scope.
		if strings.Contains(name, ".") && strings.Contains(scope, "app-flatpak-"+name+"-") {
			return name
		}
	}
	for _, name := range names {
		if len(name) >= MinCmdlineMatch && strings.Contains(p.Cmdline, name) {
			return name
		}
	}
	return ""
}

// ParseCgroupV2 достаёт путь из строки "0::/user.slice/...".
func ParseCgroupV2(data string) string {
	for _, line := range strings.Split(data, "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// ParsePPID достаёт родителя из /proc/pid/stat. Имя процесса в скобках может
// содержать пробелы и скобки, поэтому считаем от последней ")".
func ParsePPID(stat string) int {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}
