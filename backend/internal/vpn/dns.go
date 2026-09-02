package vpn

// Управление системным списком серверов имён на время соединения.
//
// Вынесено отдельно, потому что способов ровно два и выбирать между ними
// приходится в рантайме: там, где есть resolvconf, ходить в /etc/resolv.conf
// руками нельзя — он им же и управляется. Там, где его нет, править файл —
// единственный способ, и раньше клиент в этом случае просто сдавался, о чём
// писал одну строчку в лог. Последствия были тихими и неприятными: запросы
// имён продолжали уходить прежнему серверу в обход туннеля (утечка DNS), а
// маршрутизация по доменам и зонам, которой посредник имён и нужен, молча
// переставала работать целиком.

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Пути к системным файлам. Переменные, а не константы, только ради тестов:
// проверять надо именно перекладывание файлов, а писать в /etc тест не может
// и не должен.
var (
	// resolvConfPath — файл, из которого системный резолвер берёт серверы имён.
	resolvConfPath = "/etc/resolv.conf"

	// resolvBackupPath — куда откладывается прежний файл. Имя постоянное:
	// по нему же уборка после аварийного завершения находит, что вернуть.
	resolvBackupPath = "/etc/resolv.conf.awg-backup"
)

const (
	// resolvMarker — по нему свой файл отличается от чужого. Нужен там, где
	// резервной копии нет: удалять можно только то, что создали сами.
	resolvMarker = "# Создано awg-client"

	// resolvOptions включает EDNS0: без него сервер обязан уместить ответ в
	// 512 байт и всё, что длиннее, помечает как усечённое. Резолвер тогда
	// повторяет вопрос по TCP — это работает, но стоит лишнего оборота на
	// каждое имя с длинным ответом, то есть на каждый CDN.
	resolvOptions = "options edns0\n"
)

// dnsMethod — каким способом список серверов был подменён. Хранится, чтобы
// откатывать ровно то, что делали: перепутать способы значит либо оставить
// систему без DNS, либо затереть чужой файл.
type dnsMethod int

const (
	dnsNotApplied dnsMethod = iota
	dnsViaResolvconf
	dnsViaFile
)

// dnsControl подменяет серверы имён и возвращает всё назад.
// Нулевое значение готово к использованию.
type dnsControl struct {
	mu     sync.Mutex
	method dnsMethod
	ifname string
}

// Apply переводит систему на заданные серверы имён.
//
// Повторный вызов заменяет предыдущую подмену: правила маршрутизации могут
// поменяться на живом туннеле, и вместе с ними — адрес посредника имён.
func (d *dnsControl) Apply(ifname string, servers []string) error {
	if len(servers) == 0 {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// resolvconf умеет заменять свою запись сам, файл — переписывается
	// целиком, поэтому откатывать перед повторной подменой не нужно.
	if _, err := exec.LookPath("resolvconf"); err == nil {
		if err := applyViaResolvconf(ifname, servers); err != nil {
			return err
		}
		d.method, d.ifname = dnsViaResolvconf, ifname
		return nil
	}

	if err := applyViaFile(servers); err != nil {
		return err
	}
	d.method, d.ifname = dnsViaFile, ifname
	return nil
}

// Restore возвращает прежние серверы имён. Безопасна, если подмены не было.
func (d *dnsControl) Restore() {
	d.mu.Lock()
	defer d.mu.Unlock()

	method, ifname := d.method, d.ifname
	d.method, d.ifname = dnsNotApplied, ""

	var err error
	switch method {
	case dnsViaResolvconf:
		err = exec.Command("resolvconf", "-d", ifname).Run()
	case dnsViaFile:
		err = restoreResolvConf()
	default:
		return
	}

	if err != nil {
		log.Printf("Не удалось вернуть прежние серверы имён: %v", err)
	}
}

func applyViaResolvconf(ifname string, servers []string) error {
	cmd := exec.Command("resolvconf", "-a", ifname, "-m", "0", "-x")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return err
	}

	// Ошибку записи не возвращаем сразу: процесс уже запущен, и выйти, не
	// дождавшись его, значит оставить зомби.
	_, writeErr := io.WriteString(stdin, nameserverLines(servers)+resolvOptions)
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("resolvconf: %w", err)
	}
	return writeErr
}

// applyViaFile подменяет /etc/resolv.conf, сохранив прежний.
func applyViaFile(servers []string) error {
	// Резервную копию делаем один раз: при пересборке правил на живом
	// туннеле файл уже наш, и повторное сохранение затёрло бы оригинал.
	if _, err := os.Lstat(resolvBackupPath); os.IsNotExist(err) {
		if err := os.Rename(resolvConfPath, resolvBackupPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("не удалось сохранить %s: %w", resolvConfPath, err)
		}
	}

	return writeSystemFileAtomic(resolvConfPath, []byte(resolvContent(servers)), 0644)
}

// nameserverLines собирает строки со списком серверов имён.
func nameserverLines(servers []string) string {
	var sb strings.Builder
	for _, server := range servers {
		fmt.Fprintf(&sb, "nameserver %s\n", server)
	}
	return sb.String()
}

// resolvContent собирает содержимое /etc/resolv.conf целиком.
func resolvContent(servers []string) string {
	header := resolvMarker + " на время VPN-соединения.\n" +
		"# Прежний файл сохранён как " + resolvBackupPath + " и вернётся при отключении.\n"

	return header + nameserverLines(servers) + resolvOptions
}

// restoreResolvConf возвращает сохранённый файл на место.
func restoreResolvConf() error {
	if _, err := os.Lstat(resolvBackupPath); err != nil {
		if os.IsNotExist(err) {
			// Копии нет — значит, и сохранять было нечего: файла до нас в
			// системе не существовало. Свой убираем за собой, иначе система
			// останется с сервером имён, который исчез вместе с туннелем.
			return removeOwnResolvConf()
		}
		return err
	}

	// Rename поверх существующего файла неделим: системный резолвер ни на
	// мгновение не увидит отсутствующий resolv.conf.
	return os.Rename(resolvBackupPath, resolvConfPath)
}

// removeOwnResolvConf снимает файл, написанный нами, и только его. Чужой
// остаётся на месте: он мог появиться после нашей подмены — например, от
// диспетчера сети, — и удалять его значит ломать разрешение имён вместо того,
// чтобы его починить.
func removeOwnResolvConf() error {
	data, err := os.ReadFile(resolvConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if !strings.HasPrefix(string(data), resolvMarker) {
		return nil
	}

	return os.Remove(resolvConfPath)
}

// restoreOrphanedDNS возвращает серверы имён, если прошлый запуск завершился
// аварийно и не успел этого сделать.
//
// Без этого убитый по SIGKILL клиент оставлял бы систему с resolv.conf,
// указывающим на исчезнувший вместе с туннелем адрес: имена перестают
// разрешаться совсем, и связи между этим и VPN пользователю не видно.
func restoreOrphanedDNS() {
	_, backupErr := os.Lstat(resolvBackupPath)
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return
	}

	// Копии нет — прибираем разве что собственный файл, оставшийся от
	// прошлого запуска на системе, где resolv.conf до нас не было.
	if os.IsNotExist(backupErr) {
		if err := removeOwnResolvConf(); err != nil {
			log.Printf("Не удалось убрать %s, оставшийся от прошлого запуска: %v", resolvConfPath, err)
		}
		return
	}

	if err := restoreResolvConf(); err != nil {
		log.Printf("Не удалось вернуть %s после прошлого запуска: %v", resolvConfPath, err)
		return
	}
	log.Printf("Восстановлен %s, оставшийся от прошлого запуска", resolvConfPath)
}

// writeSystemFileAtomic пишет системный файл через временный с последующим
// переименованием: читатель видит либо прежнее содержимое, либо новое, но
// никогда не пустое.
func writeSystemFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""

	return nil
}
