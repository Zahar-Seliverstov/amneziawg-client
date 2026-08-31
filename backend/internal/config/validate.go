package config

// Проверка разобранной конфигурации.
//
// Раньше её не было вовсе: требовались только непустой PrivateKey и хотя бы
// один пир. Всё остальное — ключ не той длины, адрес без маски, MTU в сто
// тысяч — доезжало до ядра туннеля и до команды ip, и отказ всплывал уже
// после нажатия «Подключить», текстом от чужого кода в системном журнале.
//
// Ошибку нужно называть в тот момент, когда пользователь ещё смотрит на поле,
// куда вставил конфигурацию.

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// keyLen — длина ключа WireGuard: 32 байта, они же 44 знака base64.
const keyLen = 32

// Границы MTU. Снизу — минимум, при котором ещё собирается IPv4-пакет
// разумного размера; сверху — предел поля в заголовке. Задача не в том, чтобы
// угадать хорошее значение, а в том, чтобы отсечь опечатку вроде лишнего нуля.
const (
	minMTU = 576
	maxMTU = 65535
)

// Validate проверяет разобранную конфигурацию целиком.
func (c *AmneziaWGConfig) Validate() error {
	if err := validateKey("PrivateKey", c.Interface.PrivateKey); err != nil {
		return err
	}

	if len(c.Interface.Address) == 0 {
		return fmt.Errorf("не задан Address: без адреса интерфейс не сможет ничего отправить")
	}
	for _, addr := range c.Interface.Address {
		if _, err := netip.ParsePrefix(strings.TrimSpace(addr)); err != nil {
			return fmt.Errorf("Address %q должен быть вида 10.0.0.2/32", addr)
		}
	}

	for _, server := range c.Interface.DNS {
		if _, err := netip.ParseAddr(strings.TrimSpace(server)); err != nil {
			return fmt.Errorf("DNS %q не похож на IP-адрес", server)
		}
	}

	if c.Interface.MTU != 0 && (c.Interface.MTU < minMTU || c.Interface.MTU > maxMTU) {
		return fmt.Errorf("MTU %d вне разумных границ (%d–%d)", c.Interface.MTU, minMTU, maxMTU)
	}

	if len(c.Peers) == 0 {
		return fmt.Errorf("в конфигурации нет ни одного [Peer]")
	}
	for i := range c.Peers {
		if err := c.Peers[i].validate(); err != nil {
			return err
		}
	}

	return nil
}

func (p *PeerConfig) validate() error {
	if err := validateKey("PublicKey", p.PublicKey); err != nil {
		return err
	}
	if p.PresharedKey != "" {
		if err := validateKey("PresharedKey", p.PresharedKey); err != nil {
			return err
		}
	}

	if len(p.AllowedIPs) == 0 {
		return fmt.Errorf("у пира не задан AllowedIPs: непонятно, какой трафик отправлять в туннель")
	}
	for _, allowed := range p.AllowedIPs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(allowed)); err != nil {
			return fmt.Errorf("AllowedIPs %q должен быть вида 0.0.0.0/0", allowed)
		}
	}

	if p.Endpoint != "" {
		if err := validateEndpoint(p.Endpoint); err != nil {
			return err
		}
	}

	return nil
}

// validateKey проверяет ключ WireGuard.
//
// Длина важнее формата: строка из 44 знаков base64, декодирующаяся не в 32
// байта, выглядит как ключ и ключом не является. Ядро отвергнет её уже при
// подключении, и пользователь увидит «ядро отвергло конфигурацию» вместо
// «ключ не той длины».
func validateKey(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("не задан %s", field)
	}

	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("%s не разбирается как base64", field)
	}
	if len(raw) != keyLen {
		return fmt.Errorf("%s имеет длину %d байт вместо %d", field, len(raw), keyLen)
	}

	return nil
}

// validateEndpoint проверяет адрес сервера.
//
// Порт обязателен: без него не с чем работать ни ядру туннеля, ни замеру
// задержки. Имя хоста здесь не разрешается — сервера может не быть в сети
// прямо сейчас, и это не повод отвергать конфигурацию.
func validateEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("Endpoint %q должен быть вида server.example.com:51820", endpoint)
	}
	if host == "" {
		return fmt.Errorf("Endpoint %q без адреса сервера", endpoint)
	}

	p, err := netip.ParseAddrPort(net.JoinHostPort("0.0.0.0", port))
	if err != nil || p.Port() == 0 {
		return fmt.Errorf("Endpoint %q: %q не похоже на номер порта", endpoint, port)
	}

	return nil
}
