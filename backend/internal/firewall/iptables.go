package firewall

import (
	"fmt"
	"os/exec"
	"strconv"
)

// iptablesDriver — запасной вариант для систем без nftables.
//
// Честное предупреждение о его слабости: правила ставятся по одному, а не
// транзакцией. Если посреди установки что-то откажет, останется недоделанная
// блокировка. Поэтому порядок здесь именно такой: сначала наполняется
// отдельная цепочка, и только последним действием на неё переводят трафик из
// OUTPUT. До этого момента блокировки нет вовсе, а после — она уже полная.
type iptablesDriver struct {
	run runner
}

func (d *iptablesDriver) Name() string { return "iptables" }

// probe перечисляет цепочку OUTPUT: без прав администратора отказ приходит
// именно отсюда.
func (d *iptablesDriver) probe() error {
	return d.run("iptables", []string{"-S", "OUTPUT"}, "")
}

// families — обе версии протокола. IPv6 обязателен: сайт с записью AAAA
// утекал бы мимо туннеля при живой блокировке для IPv4.
func (d *iptablesDriver) families() []string {
	cmds := []string{"iptables"}
	if _, err := exec.LookPath("ip6tables"); err == nil {
		cmds = append(cmds, "ip6tables")
	}
	return cmds
}

func (d *iptablesDriver) Apply(r Rules) error {
	// Снимаем прежнюю: цепочка могла остаться от прошлого запуска, и правила
	// в ней сложились бы вдвое.
	if err := d.Clear(); err != nil {
		return err
	}

	for _, cmd := range d.families() {
		steps := [][]string{
			{"-N", tableName},
			{"-A", tableName, "-o", "lo", "-j", "RETURN"},
			{"-A", tableName, "-o", r.Interface, "-j", "RETURN"},
			{"-A", tableName, "-m", "mark", "--mark", strconv.FormatUint(uint64(Mark), 10), "-j", "RETURN"},
			{"-A", tableName, "-p", "udp", "--dport", "67", "-j", "RETURN"},
			{"-A", tableName, "-p", "udp", "--dport", "547", "-j", "RETURN"},
			{"-A", tableName, "-j", "DROP"},
			// Перевод трафика на цепочку — последним: до него блокировки нет,
			// после него она уже полная.
			{"-I", "OUTPUT", "1", "-j", tableName},
		}

		for _, args := range steps {
			if err := d.run(cmd, args, ""); err != nil {
				// Недоделанную блокировку оставлять нельзя: она либо не
				// защищает, либо режет лишнее.
				_ = d.Clear()
				return fmt.Errorf("%s: %w", cmd, err)
			}
		}
	}

	return nil
}

func (d *iptablesDriver) Clear() error {
	// Ошибки игнорируются намеренно: снимать приходится и вслепую — после
	// аварийного завершения прошлого запуска, ничего не зная о том, что там
	// осталось. «Цепочки нет» — это успех, а не отказ.
	for _, cmd := range d.families() {
		_ = d.run(cmd, []string{"-D", "OUTPUT", "-j", tableName}, "")
		_ = d.run(cmd, []string{"-F", tableName}, "")
		_ = d.run(cmd, []string{"-X", tableName}, "")
	}
	return nil
}
