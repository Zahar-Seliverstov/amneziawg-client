package iproute

// Проверка на настоящей системе: разбор вывода ip проверяется здесь на том,
// что печатает установленный в системе iproute2, а не на строках из головы.
// Ничего не меняет — только спрашивает. Пропускается в коротком режиме
// (go test -short) и в обязательные проверки CI не входит.

import "testing"

func liveTool(t *testing.T) Tool {
	t.Helper()

	if testing.Short() {
		t.Skip("системный тест")
	}
	if err := Available(); err != nil {
		t.Skipf("нет команды ip: %v", err)
	}
	return New()
}

// Собственный адрес машины система обязана назвать локальным: на этом
// держится отказ ставить маршрут на ответы вроде 127.0.0.1.
func TestLivePathToLoopback(t *testing.T) {
	path, err := liveTool(t).PathTo("127.0.0.1")
	if err != nil {
		t.Fatalf("не удалось спросить путь: %v", err)
	}
	if !path.Local || path.Device == "" {
		t.Errorf("получили %+v, ждали локальный путь с интерфейсом", path)
	}
}

// Путь до чужого адреса обязан приходить с интерфейсом — по нему решается,
// забрал ли адрес туннель.
func TestLivePathToRemote(t *testing.T) {
	path, err := liveTool(t).PathTo("1.1.1.1")
	if err != nil {
		t.Skipf("до внешнего адреса нет пути: %v", err)
	}
	if path.Local || path.Device == "" {
		t.Errorf("получили %+v, ждали внешний путь с интерфейсом", path)
	}
}

func TestLiveDefaultPath(t *testing.T) {
	path, err := liveTool(t).DefaultPathFor("1.1.1.1")
	if err != nil {
		t.Skipf("в системе нет маршрута по умолчанию: %v", err)
	}
	if path.Device == "" {
		t.Errorf("получили %+v, ждали интерфейс", path)
	}
}
