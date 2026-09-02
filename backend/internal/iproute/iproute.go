// Package iproute — единственное место, которое знает синтаксис команды ip.
//
// Зачем отдельный пакет. Адреса, маршруты и опрос таблицы маршрутизации
// вызывались из менеджера напрямую через exec, и проверить решения о
// маршрутах можно было только от root на живой машине: любой тест немедленно
// трогал настоящую сеть. Интерфейс Tool разрывает эту связь — что делать,
// решает vpn.Manager, а как это сказать системе, знает только этот пакет.
package iproute

import (
	"fmt"
	"os/exec"
	"strings"
)

// Tool — команда ip за интерфейсом.
type Tool interface {
	// AddAddress вешает адрес на интерфейс.
	AddAddress(prefix, dev string) error
	// LinkUp поднимает интерфейс.
	LinkUp(dev string) error
	// HasGlobalAddress сообщает, есть ли у интерфейса адрес, пригодный для
	// отправки. Локальные адреса канала не в счёт: они появляются сами.
	HasGlobalAddress(dev string) (bool, error)

	// AddRoute и DelRoute принимают хвост команды "ip route add|del" как есть:
	// набор аргументов у маршрута слишком разный (via, dev, метрики), чтобы
	// описывать его отдельным типом.
	AddRoute(args ...string) error
	DelRoute(args ...string) error

	// PathTo сообщает, каким путём система пойдёт к адресу СЕЙЧАС, со всеми
	// уже стоящими маршрутами. Спрашивать надо именно ядро: только оно знает
	// про чужие интерфейсы — другой VPN, соседнюю подсеть, мосты docker.
	PathTo(addr string) (Path, error)

	// DefaultPathFor — путь по умолчанию для семейства адреса dest. Нужен
	// там, где своего маршрута у адреса нет и «мимо туннеля» означает
	// «туда же, куда уходит всё остальное».
	DefaultPathFor(dest string) (Path, error)

	// RoutesFor возвращает маршруты, заданные ровно для этого префикса, по
	// строке на маршрут. Пустой список означает, что своего маршрута у
	// префикса нет и его судьбу решает что-то менее точное.
	RoutesFor(prefix string) ([]string, error)
}

// Available сообщает, есть ли команда ip в системе.
//
// Проверяется отдельно и заранее: без неё туннель поднимется, но ни адреса,
// ни маршруты на него не лягут — состояние «Подключено» без единого пакета.
func Available() error {
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("не найдена команда ip из пакета iproute2: %w", err)
	}
	return nil
}

// New возвращает Tool поверх настоящей команды ip.
func New() Tool {
	return &cmdTool{run: runCommand}
}

// runner выполняет команду ip и возвращает её вывод.
//
// Поле, а не прямой вызов exec: разбор вывода — самая хрупкая часть пакета,
// а проверить его иначе можно было бы только на машине с нужной сетью.
type runner func(args ...string) (string, error)

type cmdTool struct {
	run runner
}

func runCommand(args ...string) (string, error) {
	cmd := exec.Command("ip", args...)

	// CombinedOutput, а не Output: ядро объясняет отказ в stderr («Network is
	// unreachable», «File exists»), и без этого текста в журнале остаётся
	// только код возврата.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("ip %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (t *cmdTool) AddAddress(prefix, dev string) error {
	_, err := t.run("address", "add", prefix, "dev", dev)
	return err
}

func (t *cmdTool) LinkUp(dev string) error {
	_, err := t.run("link", "set", dev, "up")
	return err
}

func (t *cmdTool) HasGlobalAddress(dev string) (bool, error) {
	output, err := t.run("-o", "address", "show", "dev", dev, "scope", "global")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (t *cmdTool) AddRoute(args ...string) error {
	_, err := t.run(append([]string{"route", "add"}, args...)...)
	return err
}

func (t *cmdTool) DelRoute(args ...string) error {
	_, err := t.run(append([]string{"route", "del"}, args...)...)
	return err
}

func (t *cmdTool) RoutesFor(prefix string) ([]string, error) {
	output, err := t.run("route", "show", prefix)
	if err != nil {
		return nil, err
	}

	var routes []string
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			routes = append(routes, line)
		}
	}
	return routes, nil
}

func (t *cmdTool) PathTo(addr string) (Path, error) {
	output, err := t.run("route", "get", addr)
	if err != nil {
		return Path{}, err
	}
	return parsePath(output)
}

func (t *cmdTool) DefaultPathFor(dest string) (Path, error) {
	family := "-4"
	if isIPv6(dest) {
		family = "-6"
	}

	output, err := t.run(family, "route", "show", "default")
	if err != nil {
		return Path{}, err
	}

	path, err := parseDefaultPath(output)
	if err != nil {
		return Path{}, fmt.Errorf("%w (%s)", err, strings.TrimPrefix(family, "-"))
	}
	return path, nil
}
