// Package firewall блокирует трафик, который пошёл бы мимо туннеля.
//
// Зачем. Когда туннель обрывается, маршруты на него исчезают вместе с
// интерфейсом, и пакеты, которые шли внутрь VPN, начинают уходить напрямую —
// открыто и с настоящего адреса. Пользователь этого не замечает: в интерфейсе
// «Переподключение», а трафик уже снаружи. Блокировка закрывает эту щель,
// поэтому и взводится вместе с маршрутами, а снимается только по явному
// отключению.
//
// Как. Тот же приём, что у wg-quick: собственные пакеты туннеля помечаются
// меткой на сокете (fwmark), и наружу выпускается только то, что помечено,
// ушло в сам туннель или в петлю. Метка избавляет от необходимости знать
// адрес сервера: он может смениться при повторном разрешении имени, и правило
// по адресу протухло бы молча.
//
// Когда блокировка применима, а когда обрезала бы разрешённое, решает не этот
// пакет — см. killSwitchApplicable в internal/vpn.
package firewall

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Причины, по которым блокировать нечем. Отдельные значения, а не строки:
// интерфейсу нужно короткое объяснение, а журналу — подробности вроде вывода
// самой команды, и смешивать их в одном тексте значит показать пользователю
// то, что ему ничего не говорит.
var (
	ErrNotSupported = errors.New("в системе нет ни nft, ни iptables")
	ErrNoPermission = errors.New("нужны права администратора")
)

// Reason — короткое объяснение недоступности, пригодное для интерфейса.
func Reason(err error) string {
	switch {
	case errors.Is(err, ErrNoPermission):
		return ErrNoPermission.Error()
	case errors.Is(err, ErrNotSupported):
		return ErrNotSupported.Error()
	default:
		return "недоступна"
	}
}

// Mark — метка, которую ядро туннеля ставит на свои исходящие пакеты.
//
// Значение произвольное, но обязано совпадать с тем, что уходит в UAPI:
// именно по нему блокировка отличает зашифрованный трафик к серверу VPN от
// всего остального. Ноль означал бы «метки нет» и пропускал бы всё подряд.
const Mark uint32 = 0xca6c

// tableName — имя таблицы (для nft) и цепочки (для iptables). Своё, отдельное:
// чужие правила мы не трогаем, а свои снимаем целиком.
const tableName = "awg_killswitch"

// Rules описывает, что разрешено выпускать наружу, пока блокировка действует.
type Rules struct {
	// Interface — интерфейс туннеля: через него можно всё.
	Interface string
}

// Driver ставит и снимает блокировку средствами конкретной подсистемы.
type Driver interface {
	// probe проверяет, что подсистемой действительно можно пользоваться.
	// Наличия команды в PATH мало: nft и iptables ставятся почти везде, но
	// работают только с правами администратора, и без проверки клиент
	// объявлял бы блокировку доступной там, где она молча не сработает.
	probe() error
	// Name — чем блокируем; попадает в объяснение для пользователя.
	Name() string
	// Apply включает блокировку. Повторный вызов заменяет её целиком, а не
	// накладывает вторую поверх.
	Apply(Rules) error
	// Clear снимает блокировку. Вызов без действующей блокировки безопасен:
	// после аварийного завершения убирать приходится вслепую.
	Clear() error
}

// runner выполняет системную команду.
//
// Поле, а не прямой вызов exec: иначе проверить, какие именно правила
// ставятся, можно было бы только от root на живой машине — то есть никак.
type runner func(name string, args []string, stdin string) error

// Detect выбирает, чем блокировать на этой машине.
//
// nftables предпочтительнее: правила применяются одной неделимой
// транзакцией, поэтому половины блокировки не бывает. iptables остаётся
// запасным вариантом и ставит правила по одному.
func Detect() (Driver, error) {
	candidates := []struct {
		command string
		driver  Driver
	}{
		{"nft", &nftDriver{run: runCommand}},
		{"iptables", &iptablesDriver{run: runCommand}},
	}

	var found bool
	var lastErr error

	for _, c := range candidates {
		if _, err := exec.LookPath(c.command); err != nil {
			continue
		}
		found = true

		if err := c.driver.probe(); err != nil {
			lastErr = err
			continue
		}
		return c.driver, nil
	}

	if !found {
		return nil, ErrNotSupported
	}
	return nil, fmt.Errorf("%w: %v", ErrNoPermission, lastErr)
}

func runCommand(name string, args []string, stdin string) error {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
