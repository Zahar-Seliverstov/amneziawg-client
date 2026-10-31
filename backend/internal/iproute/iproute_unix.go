//go:build !windows

// Реализация Tool для Linux — поверх команды ip из iproute2.

package iproute

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

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
	return &cmdTool{run: runCommand, runBatch: runBatchCommand}
}

// runner выполняет команду ip и возвращает её вывод.
//
// Поле, а не прямой вызов exec: разбор вывода — самая хрупкая часть пакета,
// а проверить его иначе можно было бы только на машине с нужной сетью.
type runner func(args ...string) (string, error)

// batchRunner скармливает команде ip готовый список команд, по одной на
// строку. Отдельно от runner, потому что здесь ввод идёт не аргументами, а
// потоком: список маршрутов не помещается в командную строку и не должен.
type batchRunner func(script string) (string, error)

type cmdTool struct {
	run      runner
	runBatch batchRunner
}

func runCommand(args ...string) (string, error) {
	cmd := exec.Command("ip", args...)

	// CombinedOutput, а не Output: ядро объясняет отказ в stderr («Network is
	// unreachable», «File exists»), и без этого текста в журнале остаётся
	// только код возврата.
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if strings.Contains(text, "File exists") {
			err = errors.Join(ErrExists, err)
		}
		return string(output), fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, text)
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

func (t *cmdTool) AddRoutes(routes [][]string) []error {
	return t.batch("add", routes)
}

func (t *cmdTool) DelRoutes(routes [][]string) []error {
	return t.batch("del", routes)
}

// batch выполняет список однотипных операций над маршрутами одним запуском.
//
// Ключ -force обязателен: без него ip прекращает работу на первой же неудаче,
// а неудачи здесь ожидаемы и не смертельны — "File exists" на маршруте,
// который уже стоит, "No such process" на снятом кем-то ещё. С -force он
// доделывает список и сообщает о каждой неудаче отдельной строкой
// "Command failed -:N", по которой и разбирается, что именно не легло.
func (t *cmdTool) batch(op string, routes [][]string) []error {
	errs := make([]error, len(routes))
	if len(routes) == 0 {
		return errs
	}

	// Один маршрут пакетом гнать незачем: обычный вызов и короче, и даёт
	// внятный текст ошибки вместо номера строки.
	if len(routes) == 1 {
		if op == "add" {
			errs[0] = t.AddRoute(routes[0]...)
		} else {
			errs[0] = t.DelRoute(routes[0]...)
		}
		return errs
	}

	var script strings.Builder
	for _, args := range routes {
		script.WriteString("route ")
		script.WriteString(op)
		script.WriteString(" ")
		script.WriteString(strings.Join(args, " "))
		script.WriteString("\n")
	}

	output, err := t.runBatch(script.String())
	if err == nil {
		return errs
	}

	failed := parseBatchFailures(output)

	// Ни одной опознанной строки, а команда всё же отказала — значит отказала
	// она целиком: не нашлась, не приняла ключ, не прочитала ввод. Считаем
	// неудавшимися все: записать в учёт маршрут, которого нет, опаснее, чем
	// не записать поставленный.
	if len(failed) == 0 {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}

	for _, line := range failed {
		if line >= 1 && line <= len(routes) {
			errs[line-1] = fmt.Errorf("ip route %s %s: %w",
				op, strings.Join(routes[line-1], " "), err)
		}
	}
	return errs
}

// batchFailure выхватывает номер строки из "Command failed -:3".
var batchFailure = regexp.MustCompile(`Command failed [^:]*:(\d+)`)

// parseBatchFailures возвращает номера строк, на которых ip споткнулся.
// Нумерация с единицы — так их печатает сам ip.
func parseBatchFailures(output string) []int {
	var lines []int

	for _, match := range batchFailure.FindAllStringSubmatch(output, -1) {
		n, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		lines = append(lines, n)
	}

	return lines
}

func runBatchCommand(script string) (string, error) {
	cmd := exec.Command("ip", "-force", "-batch", "-")
	cmd.Stdin = strings.NewReader(script)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("ip -batch: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
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
