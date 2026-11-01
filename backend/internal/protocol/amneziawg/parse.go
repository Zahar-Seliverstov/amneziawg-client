package amneziawg

// Разбор и проверка .conf.
//
// Проверка — целиком и сразу: всё, что не отсеяно здесь, всплывёт уже после
// нажатия «Подключить» текстом от ядра или от команды ip, в журнале, которого
// пользователь не видит. Ошибку нужно назвать, пока он смотрит на поле, куда
// вставил конфигурацию.

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Границы MTU. Задача не угадать хорошее значение, а отсечь опечатку вроде
// лишнего нуля.
const (
	minMTU = 576
	maxMTU = 65535
)

// Parse разбирает и проверяет текст .conf.
func Parse(raw string) (*Config, error) {
	cfg := &Config{}
	var (
		section string
		peer    *Peer
	)

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}

		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			section = strings.ToLower(strings.TrimSpace(text[1 : len(text)-1]))
			if section == "peer" {
				cfg.Peers = append(cfg.Peers, Peer{})
				peer = &cfg.Peers[len(cfg.Peers)-1]
			}
			continue
		}

		name, value, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)

		var err error
		switch section {
		case "interface":
			err = cfg.Interface.set(name, value)
		case "peer":
			peer.set(name, value)
		}
		if err != nil {
			return nil, fmt.Errorf("строка %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("не удалось прочитать конфигурацию: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// set разбирает поле [Interface]. Незнакомые поля (ListenPort, PostUp и
// прочее из wg-quick) пропускаются: к подключению они не относятся.
func (i *Interface) set(name, value string) error {
	switch key := strings.ToLower(name); key {
	case "privatekey":
		i.PrivateKey = value
	case "address":
		i.Addresses = append(i.Addresses, splitList(value)...)
	case "dns":
		i.DNS = append(i.DNS, splitList(value)...)
	case "mtu":
		mtu, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("MTU = %q: ожидалось целое число", value)
		}
		i.MTU = mtu
	default:
		spec, known := params[key]
		if !known {
			return nil
		}
		p, err := parseParam(spec, name, value)
		if err != nil {
			return err
		}
		i.Obfuscation = append(i.Obfuscation, p)
	}
	return nil
}

func (p *Peer) set(name, value string) {
	switch strings.ToLower(name) {
	case "publickey":
		p.PublicKey = value
	case "presharedkey":
		p.PresharedKey = value
	case "endpoint":
		p.Endpoint = value
	case "allowedips":
		p.AllowedIPs = append(p.AllowedIPs, splitList(value)...)
	case "persistentkeepalive":
		p.Keepalive = value
	}
}

func (c *Config) validate() error {
	if err := checkKey("PrivateKey", c.Interface.PrivateKey); err != nil {
		return err
	}

	if len(c.Interface.Addresses) == 0 {
		return fmt.Errorf("не задан Address: без адреса интерфейс не сможет ничего отправить")
	}
	for _, addr := range c.Interface.Addresses {
		if _, err := netip.ParsePrefix(addr); err != nil {
			return fmt.Errorf("Address %q должен быть вида 10.0.0.2/32", addr)
		}
	}

	for _, server := range c.Interface.DNS {
		if _, err := netip.ParseAddr(server); err != nil {
			return fmt.Errorf("DNS %q не похож на IP-адрес", server)
		}
	}

	if mtu := c.Interface.MTU; mtu != 0 && (mtu < minMTU || mtu > maxMTU) {
		return fmt.Errorf("MTU %d вне разумных границ (%d–%d)", mtu, minMTU, maxMTU)
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

func (p *Peer) validate() error {
	if err := checkKey("PublicKey", p.PublicKey); err != nil {
		return err
	}
	if p.PresharedKey != "" {
		if err := checkKey("PresharedKey", p.PresharedKey); err != nil {
			return err
		}
	}

	if len(p.AllowedIPs) == 0 {
		return fmt.Errorf("у пира не задан AllowedIPs: непонятно, какой трафик отправлять в туннель")
	}
	for _, allowed := range p.AllowedIPs {
		if _, err := netip.ParsePrefix(allowed); err != nil {
			return fmt.Errorf("AllowedIPs %q должен быть вида 0.0.0.0/0", allowed)
		}
	}

	if p.Endpoint != "" {
		if err := checkEndpoint(p.Endpoint); err != nil {
			return err
		}
	}

	if p.Keepalive != "" {
		if err := checkRange(p.Keepalive); err != nil {
			return fmt.Errorf("PersistentKeepalive = %q: %w", p.Keepalive, err)
		}
	}
	return nil
}

// checkEndpoint проверяет адрес сервера. Порт обязателен; имя хоста не
// разрешается — сервера может не быть в сети прямо сейчас, и это не повод
// отвергать конфигурацию.
func checkEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("Endpoint %q должен быть вида server.example.com:51820", endpoint)
	}
	if host == "" {
		return fmt.Errorf("Endpoint %q без адреса сервера", endpoint)
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		return fmt.Errorf("Endpoint %q: %q не похоже на номер порта", endpoint, port)
	}
	return nil
}

// splitList разбирает список через запятую, пропуская пустые элементы.
func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
