package firewall

import "fmt"

// nftDriver блокирует трафик через nftables.
//
// Весь набор правил уходит одной транзакцией `nft -f -`: nftables применяет
// её целиком или не применяет вовсе. Половины блокировки — когда часть правил
// встала, а разрешающие не успели, — не бывает, а это ровно та половина,
// которая оставила бы машину без сети.
type nftDriver struct {
	run runner
}

func (d *nftDriver) Name() string { return "nftables" }

// probe читает список таблиц: обращение к netlink без нужных прав отказывает
// именно здесь, а не молча при установке правил.
func (d *nftDriver) probe() error {
	return d.run("nft", []string{"list", "tables"}, "")
}

func (d *nftDriver) Apply(r Rules) error {
	return d.run("nft", []string{"-f", "-"}, d.ruleset(r))
}

func (d *nftDriver) Clear() error {
	return d.run("nft", []string{"-f", "-"}, clearScript)
}

// clearScript снимает таблицу, даже если её нет.
//
// `delete table` на отсутствующей таблице — ошибка, а снимать приходится и
// вслепую: после SIGKILL прошлого запуска правила остаются, и убрать их надо
// при следующем старте, ничего не зная о них. Пара «создать, затем удалить»
// делает вызов безобидным в обоих случаях.
const clearScript = "add table inet " + tableName + "\n" +
	"delete table inet " + tableName + "\n"

// ruleset собирает набор правил.
//
// Семейство inet, а не ip: одна таблица покрывает и IPv4, и IPv6. Отдельная
// таблица для IPv6 забывается ровно тогда, когда она нужна, — сайт с записью
// AAAA утёк бы мимо туннеля при живой блокировке для IPv4.
func (d *nftDriver) ruleset(r Rules) string {
	return clearScript + fmt.Sprintf(`table inet %s {
	chain output {
		type filter hook output priority 0; policy drop;

		# Петля: через неё идёт и собственный API, и обращения к своим же
		# адресам — ядро направляет их в lo, а не наружу.
		oifname "lo" accept

		# Сам туннель: всё, что в него ушло, уже зашифровано.
		oifname %q accept

		# Зашифрованные пакеты к серверу VPN. Метку ставит ядро туннеля на
		# свой сокет; по адресу это правило писать нельзя — адрес сервера
		# может смениться при повторном разрешении имени.
		meta mark %#x accept

		# Аренда адреса. wg-quick это не разрешает, и на ноутбуке с коротким
		# сроком аренды соединение отваливается само по себе — блокировка не
		# должна отбирать у машины адрес, который ей выдала сеть.
		udp dport 67 accept
		udp dport 547 accept
	}
}
`, tableName, r.Interface, Mark)
}
