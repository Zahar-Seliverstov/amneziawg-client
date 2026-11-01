package vpn

// NRPT (Name Resolution Policy Table) — правило «все имена спрашивать только
// у этих серверов».
//
// Без него серверы имён туннеля — лишь одни из нескольких: Windows по
// умолчанию спрашивает DNS всех адаптеров параллельно (smart multi-homed
// name resolution) и берёт первый ответ. Ответ роутера часто приходит раньше
// туннельного — и тогда наш посредник имени не видит, маршрут по правилу не
// ставится, а сайт из списка «только через VPN» открывается напрямую.
//
// Правило лежит в реестре службы DNS-клиента под нашим собственным ключом:
// снимается удалением ключа и не задевает чужие правила. Пережив аварийное
// завершение, оно оставило бы систему без DNS (сервер — адрес исчезнувшего
// туннеля), поэтому служба снимает его и при старте (restoreOrphanedDNS).

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	nrptParent = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	// nrptKey — постоянный идентификатор нашего правила.
	nrptKey = `{6A1C2D1E-5C1A-4E57-9B6F-AC6C1E7A0001}`

	nrptGenericDNS = 0x8 // ConfigOptions: правило задаёт серверы имён
	nrptVersion    = 2
)

var (
	userenv          = windows.NewLazySystemDLL("userenv.dll")
	procRefresh      = userenv.NewProc("RefreshPolicyEx")
	dnsapi           = windows.NewLazySystemDLL("dnsapi.dll")
	procFlushResolve = dnsapi.NewProc("DnsFlushResolverCache")
)

// setNRPT направляет разрешение всех имён на servers.
func setNRPT(servers []string) error {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, nrptParent+`\`+nrptKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("NRPT: %w", err)
	}
	defer key.Close()

	err = errors.Join(
		key.SetStringsValue("Name", []string{"."}),
		key.SetStringValue("GenericDNSServers", strings.Join(servers, ";")),
		key.SetStringValue("IPSECCARestriction", ""),
		key.SetDWordValue("ConfigOptions", nrptGenericDNS),
		key.SetDWordValue("Version", nrptVersion),
	)
	if err != nil {
		return fmt.Errorf("NRPT: %w", err)
	}
	refreshDNSPolicy()
	return nil
}

// removeNRPT снимает наше правило. Его отсутствие — не ошибка.
func removeNRPT() error {
	err := registry.DeleteKey(registry.LOCAL_MACHINE, nrptParent+`\`+nrptKey)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("NRPT: %w", err)
	}
	if err == nil {
		refreshDNSPolicy()
	}
	return nil
}

// refreshDNSPolicy просит DNS-клиент перечитать правила. Без этого он
// подхватил бы их только при следующем применении групповых политик.
func refreshDNSPolicy() {
	const rpForce = 1
	if procRefresh.Find() == nil {
		procRefresh.Call(1, rpForce) // 1 — политики компьютера
	}
	flushDNSCache()
}

// flushDNSCache сбрасывает кэш DNS-клиента: адрес, выясненный до запуска
// посредника, иначе отдавался бы из кэша, и маршрут по правилу не вставал.
func flushDNSCache() {
	if procFlushResolve.Find() == nil {
		procFlushResolve.Call()
	}
}
