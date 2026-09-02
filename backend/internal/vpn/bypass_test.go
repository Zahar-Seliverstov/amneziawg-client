package vpn

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/iproute"
)

// fakeIP подменяет команду ip: запоминает поставленные и снятые маршруты и
// отвечает заранее заданными путями.
type fakeIP struct {
	paths    map[string]iproute.Path
	pathErr  map[string]error
	def      iproute.Path
	defErr   error
	added    [][]string
	deleted  [][]string
	addFails bool
	// routes — что печатает "ip route show <префикс>".
	routes map[string][]string
}

func (f *fakeIP) AddAddress(prefix, dev string) error { return nil }
func (f *fakeIP) LinkUp(dev string) error             { return nil }
func (f *fakeIP) HasGlobalAddress(dev string) (bool, error) {
	return true, nil
}

func (f *fakeIP) AddRoute(args ...string) error {
	if f.addFails {
		return errors.New("отказ по условию теста")
	}
	f.added = append(f.added, args)
	return nil
}

func (f *fakeIP) DelRoute(args ...string) error {
	f.deleted = append(f.deleted, args)
	return nil
}

func (f *fakeIP) PathTo(addr string) (iproute.Path, error) {
	if err, ok := f.pathErr[addr]; ok {
		return iproute.Path{}, err
	}
	if path, ok := f.paths[addr]; ok {
		return path, nil
	}
	// Ничего своего у адреса нет — его забрал туннель.
	return iproute.Path{Device: "awg0"}, nil
}

func (f *fakeIP) DefaultPathFor(dest string) (iproute.Path, error) {
	return f.def, f.defErr
}

func (f *fakeIP) RoutesFor(prefix string) ([]string, error) {
	return f.routes[prefix], nil
}

func (f *fakeIP) joinedAdded() []string {
	out := make([]string, 0, len(f.added))
	for _, args := range f.added {
		out = append(out, strings.Join(args, " "))
	}
	return out
}

func managerWith(ip iproute.Tool) *Manager {
	return &Manager{interfaceName: "awg0", ip: ip}
}

func homeGateway() iproute.Path {
	return iproute.Path{Gateway: "192.168.0.1", Device: "wlan0"}
}

// Регрессия. Адрес, до которого система дотягивается другим путём — вторым
// VPN, соседней подсетью, мостом docker, — трогать нельзя: его собственный
// маршрут специфичнее наших /1 и выигрывает сам.
//
// Раньше клиент ставил на такой адрес маршрут через шлюз по умолчанию и этим
// ломал ровно то, что правило должно было починить: сайт корпоративной сети
// за вторым туннелем переставал открываться при включённом VPN.
func TestBypassRouteLeavesForeignPathAlone(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{
			"192.168.96.28": {Gateway: "192.168.35.1", Device: "tun0"},
		},
		def: homeGateway(),
	}

	args, needed, err := managerWith(ip).bypassRoute("192.168.96.28/32")
	if err != nil {
		t.Fatalf("подбор пути не удался: %v", err)
	}
	if needed {
		t.Fatalf("маршрут признан нужным (%v), хотя система и так ведёт мимо туннеля", args)
	}
}

// Адрес, у которого своего маршрута нет, забирает туннель. Вот его и надо
// вывести — туда же, куда уходит весь остальной трафик.
func TestBypassRouteUsesDefaultPath(t *testing.T) {
	ip := &fakeIP{def: homeGateway()}

	args, needed, err := managerWith(ip).bypassRoute("1.2.3.4/32")
	if err != nil {
		t.Fatalf("подбор пути не удался: %v", err)
	}
	if !needed {
		t.Fatal("маршрут не поставлен, хотя адрес забрал туннель")
	}
	if got, want := strings.Join(args, " "), "1.2.3.4/32 via 192.168.0.1 dev wlan0"; got != want {
		t.Errorf("получили %q, ждали %q", got, want)
	}
}

// Ответ 127.0.0.1 отдают блокирующие серверы имён. Маршрут на собственный
// адрес машины бессмысленен: локальная таблица просматривается раньше всех.
func TestBypassRouteSkipsLocalAddress(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"127.0.0.1": {Device: "lo", Local: true}},
		def:   homeGateway(),
	}

	if _, needed, err := managerWith(ip).bypassRoute("127.0.0.1/32"); err != nil || needed {
		t.Errorf("получили needed=%v, err=%v; ждали, что маршрут не понадобится", needed, err)
	}
}

// Маршрут, поставленный для исключения, обязан попасть в учёт: без этого он
// останется в таблице после смены правил и продолжит выводить трафик мимо
// туннеля.
func TestAddBypassRouteIsRecordedForFlush(t *testing.T) {
	ip := &fakeIP{def: homeGateway()}
	m := managerWith(ip)

	if err := m.addBypassRoute("1.2.3.4/32"); err != nil {
		t.Fatalf("исключение не поставлено: %v", err)
	}
	if len(m.installedRoutes) != 1 {
		t.Fatalf("в учёте %d маршрутов, ждали 1", len(m.installedRoutes))
	}

	m.flushRoutes()
	if len(ip.deleted) != 1 || strings.Join(ip.deleted[0], " ") != "1.2.3.4/32 via 192.168.0.1 dev wlan0" {
		t.Errorf("снято %v, ждали ровно поставленный маршрут", ip.deleted)
	}
}

// А вот адрес, который и так шёл мимо туннеля, в учёт попасть не должен:
// снимать чужой маршрут мы не вправе.
func TestAddBypassRouteRecordsNothingWhenRouteIsNotNeeded(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"192.168.96.28": {Gateway: "192.168.35.1", Device: "tun0"}},
		def:   homeGateway(),
	}
	m := managerWith(ip)

	if err := m.addBypassRoute("192.168.96.28/32"); err != nil {
		t.Fatalf("исключение не обработано: %v", err)
	}
	if len(ip.added) != 0 {
		t.Errorf("поставлены маршруты %v, а не должно быть ни одного", ip.joinedAdded())
	}
	if len(m.installedRoutes) != 0 {
		t.Errorf("в учёте %d маршрутов, ждали 0", len(m.installedRoutes))
	}
}

func TestBypassInstallerRemovesExactlyWhatItAdded(t *testing.T) {
	ip := &fakeIP{def: homeGateway()}
	installer := newBypassInstaller(managerWith(ip))
	prefix := netip.MustParsePrefix("1.2.3.4/32")

	if err := installer.Add(prefix); err != nil {
		t.Fatalf("маршрут не поставлен: %v", err)
	}

	// Путь наружу сменился (Wi-Fi на кабель) — снимать надо всё равно тот
	// маршрут, который ставили, иначе он останется в таблице навсегда.
	ip.def = iproute.Path{Gateway: "10.0.0.1", Device: "enp4s0"}

	if err := installer.Remove(prefix); err != nil {
		t.Fatalf("маршрут не снят: %v", err)
	}
	if len(ip.deleted) != 1 || strings.Join(ip.deleted[0], " ") != "1.2.3.4/32 via 192.168.0.1 dev wlan0" {
		t.Errorf("снято %v, ждали маршрут через прежний шлюз", ip.deleted)
	}
}

// Адрес, которому маршрут не понадобился, всё равно берётся на учёт: иначе
// клиент опрашивал бы ядро на каждый ответ DNS. Снятие при этом не должно
// трогать таблицу — маршрут там не наш.
func TestBypassInstallerRemembersAdoptedAddress(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"192.168.96.28": {Gateway: "192.168.35.1", Device: "tun0"}},
		def:   homeGateway(),
	}
	installer := newBypassInstaller(managerWith(ip))
	prefix := netip.MustParsePrefix("192.168.96.28/32")

	if err := installer.Add(prefix); err != nil {
		t.Fatalf("адрес не обработан: %v", err)
	}
	if len(ip.added) != 0 {
		t.Errorf("поставлены маршруты %v, а не должно быть ни одного", ip.joinedAdded())
	}

	if err := installer.Remove(prefix); err != nil {
		t.Fatalf("снятие не удалось: %v", err)
	}
	if len(ip.deleted) != 0 {
		t.Errorf("снят чужой маршрут %v", ip.deleted)
	}
}

func TestBypassInstallerRefusesToRemoveForeignRoute(t *testing.T) {
	installer := newBypassInstaller(managerWith(&fakeIP{def: homeGateway()}))

	if err := installer.Remove(netip.MustParsePrefix("1.2.3.4/32")); err == nil {
		t.Error("снятие незнакомого маршрута должно быть ошибкой")
	}
}

// Неудача при постановке не должна оставлять запись в учёте: иначе снятие
// попыталось бы убрать несуществующий маршрут, а повторная попытка поставить
// его не состоялась бы вовсе.
func TestBypassInstallerForgetsFailedRoute(t *testing.T) {
	ip := &fakeIP{def: homeGateway(), addFails: true}
	installer := newBypassInstaller(managerWith(ip))
	prefix := netip.MustParsePrefix("1.2.3.4/32")

	if err := installer.Add(prefix); err == nil {
		t.Fatal("отказ постановки должен возвращаться наружу")
	}
	if err := installer.Remove(prefix); err == nil {
		t.Error("несостоявшийся маршрут не должен числиться поставленным")
	}
}

// Регрессия. Маршрут к серверу VPN мог уже стоять — его оставил аварийно
// завершившийся прошлый запуск или другой клиент к тому же серверу. Отказ
// команды "ip route add" тогда означает не беду, а «уже сделано».
//
// Раньше такой маршрут валил настройку полного туннеля целиком: клиент
// показывал «Подключено», а маршрутов в туннель не ставил ни одного, и весь
// трафик шёл мимо него.
func TestPinnedOutsideTunnel(t *testing.T) {
	m := managerWith(&fakeIP{routes: map[string][]string{
		"144.31.144.229/32": {"144.31.144.229 via 192.168.0.1 dev wlan0"},
		"1.2.3.4/32":        {"1.2.3.4 dev awg0"},
	}})

	if !m.pinnedOutsideTunnel("144.31.144.229/32") {
		t.Error("чужой маршрут мимо туннеля должен считаться выводом наружу")
	}
	if m.pinnedOutsideTunnel("1.2.3.4/32") {
		t.Error("маршрут в сам туннель выводом наружу не является")
	}
	if m.pinnedOutsideTunnel("8.8.8.8/32") {
		t.Error("без собственного маршрута адрес не пришпилен")
	}
}
