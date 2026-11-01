package appsteer

import (
	"net/netip"
	"sync"
	"time"
)

// Verdict — что делать с перехваченным пакетом.
type Verdict int

const (
	// Pass — отправить дальше как есть.
	Pass Verdict = iota
	// Rewritten — пакет переписан: исходящий отправить заново (его поведут
	// маршруты нового адреса), входящий — доставить через интерфейс
	// исходного адреса.
	Rewritten
	// Drop — выбросить.
	Drop
)

// Config — между какими адресами переводить соединения программ.
type Config struct {
	// From — адрес интерфейса, которым соединения идут без нас (IPv4).
	From netip.Addr
	// To — адрес интерфейса, куда их нужно увести (IPv4).
	To netip.Addr
	// MSS — потолок MSS для уведённых соединений; 0 — не трогать.
	MSS uint16
	// DropIPv6 — новые соединения программ по IPv6 не выпускать. Нужно,
	// когда программу ведём в туннель, а у туннеля нет IPv6: иначе она ушла
	// бы по IPv6 напрямую. Отказ заставляет программу перейти на IPv4.
	DropIPv6 bool
}

// OwnerFunc находит процесс, которому принадлежит локальный адрес.
type OwnerFunc func(proto uint8, local netip.AddrPort) (pid uint32, ok bool)

// MatchFunc сообщает, нужно ли уводить соединение этого процесса по этому
// протоколу (правило app:имя — любое, app:имя/udp — только UDP).
type MatchFunc func(proto uint8, pid uint32) bool

// Steerer принимает решения по пакетам и правит их. Безопасен для
// одновременного вызова из нескольких горутин.
type Steerer struct {
	cfg   Config
	owner OwnerFunc
	match MatchFunc
	now   func() time.Time

	mu    sync.Mutex
	flows map[flowKey]*flow
}

// flowKey — соединение с точки зрения программы: локальный порт и удалённый
// адрес. Локальный адрес не нужен — он всегда From.
type flowKey struct {
	proto  uint8
	local  uint16
	remote netip.AddrPort
}

type flow struct {
	steer bool
	seen  time.Time
}

// Сколько помнить молчащее соединение. TCP живёт долго и может молчать
// (keepalive раз в два часа — крайний случай, обычно чаще); UDP «соединения»
// без обмена — нет.
const (
	tcpIdle = 2 * time.Hour
	udpIdle = 3 * time.Minute
)

// New создаёт Steerer.
func New(cfg Config, owner OwnerFunc, match MatchFunc) *Steerer {
	return &Steerer{cfg: cfg, owner: owner, match: match, now: time.Now, flows: make(map[flowKey]*flow)}
}

// Outbound решает судьбу исходящего пакета и, если нужно, переписывает его.
func (s *Steerer) Outbound(p []byte) Verdict {
	h, ok := parse(p)
	if !ok {
		return Pass
	}

	if h.v6 {
		if s.cfg.DropIPv6 && opens(h) && s.isApp(h) {
			return Drop
		}
		return Pass
	}
	if h.src != s.cfg.From {
		return Pass
	}

	key := flowKey{h.proto, h.sport, netip.AddrPortFrom(h.dst, h.dport)}
	f := s.lookup(key, h)
	if f == nil || !f.steer {
		return Pass
	}

	setSrc(p, s.cfg.To)
	clampMSS(p, h, s.cfg.MSS)
	fixChecksums(p, h)
	s.after(key, h)
	return Rewritten
}

// Inbound переписывает ответ, пришедший на адрес To по уведённому
// соединению, обратно на From.
func (s *Steerer) Inbound(p []byte) Verdict {
	h, ok := parse(p)
	if !ok || h.v6 || h.dst != s.cfg.To {
		return Pass
	}

	key := flowKey{h.proto, h.dport, netip.AddrPortFrom(h.src, h.sport)}
	s.mu.Lock()
	f := s.flows[key]
	if f != nil {
		f.seen = s.now()
	}
	s.mu.Unlock()
	if f == nil || !f.steer {
		return Pass
	}

	setDst(p, s.cfg.From)
	clampMSS(p, h, s.cfg.MSS)
	fixChecksums(p, h)
	s.after(key, h)
	return Rewritten
}

// lookup находит соединение, а для только что открываемого — решает, чьё
// оно. nil — соединение началось до нас: его не трогаем, переводить живое
// соединение на другой адрес бессмысленно, его просто оборвало бы.
func (s *Steerer) lookup(key flowKey, h header) *flow {
	now := s.now()

	s.mu.Lock()
	f := s.flows[key]
	// Новый SYN по известному ключу — то же сочетание портов заново: решаем
	// заново, программа могла смениться.
	fresh := f == nil || (h.proto == protoTCP && h.flags&(tcpSYN|tcpACK) == tcpSYN)
	if !fresh {
		f.seen = now
	}
	s.mu.Unlock()

	if !fresh {
		return f
	}
	if !opens(h) {
		return nil
	}

	// Владельца спрашиваем вне замка: это вопрос к системе.
	f = &flow{steer: s.isApp(h), seen: now}

	s.mu.Lock()
	s.flows[key] = f
	s.mu.Unlock()
	return f
}

// opens сообщает, что пакет открывает соединение: SYN у TCP, любой пакет у
// UDP (у него нет рукопожатия).
func opens(h header) bool {
	if h.proto == protoTCP {
		return h.flags&(tcpSYN|tcpACK) == tcpSYN
	}
	return true
}

func (s *Steerer) isApp(h header) bool {
	pid, ok := s.owner(h.proto, netip.AddrPortFrom(h.src, h.sport))
	return ok && s.match(h.proto, pid)
}

// after забывает соединение, оборванное RST: следующий пакет с тем же
// сочетанием портов — уже новое соединение.
func (s *Steerer) after(key flowKey, h header) {
	if h.proto == protoTCP && h.flags&tcpRST != 0 {
		s.mu.Lock()
		delete(s.flows, key)
		s.mu.Unlock()
	}
}

// Sweep забывает соединения, молчащие дольше срока. Вызывать периодически.
func (s *Steerer) Sweep() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, f := range s.flows {
		idle := udpIdle
		if key.proto == protoTCP {
			idle = tcpIdle
		}
		if now.Sub(f.seen) > idle {
			delete(s.flows, key)
		}
	}
}

// Flows — сколько соединений сейчас уводится (для журнала).
func (s *Steerer) Flows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.flows {
		if f.steer {
			n++
		}
	}
	return n
}
