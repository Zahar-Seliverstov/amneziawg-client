package appsteer

// Работающий перехват на Windows: два канала WinDivert и учёт процессов.
//
//   - исходящий: пакеты с адресом From. Новые соединения выбранных программ
//     получают адрес To и уходят заново — система ведёт их маршрутами того
//     интерфейса, которому принадлежит To (строгая модель хоста Windows);
//   - входящий: пакеты на адрес To. Ответы по уведённым соединениям получают
//     адрес From и доставляются через интерфейс From — программа видит их
//     так, будто соединение шло обычным путём.
//
// Остальные пакеты возвращаются в стек нетронутыми. Если служба перестанет
// их забирать, драйвер через две секунды выпускает их сам, так что
// зависание здесь не оставит машину без сети навсегда.

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/amnezia-web-client/internal/apps"
)

// Programs — набор программ: All — все соединения, UDP — только UDP.
type Programs struct {
	All, UDP []string
}

// Session — включённый перехват.
type Session struct {
	opts    Options
	steer   *Steerer
	out, in handle

	programs atomic.Pointer[Programs]
	procs    procCache

	// fromIf — интерфейс адреса From: туда доставляются переписанные
	// ответы. Узнаётся по исходящим пакетам — они идут именно через него.
	fromIf atomic.Pointer[address]

	stop chan struct{}
	wg   sync.WaitGroup
}

// Start включает перехват.
func Start(opts Options, programs Programs) (*Session, error) {
	if !opts.From.Is4() || !opts.To.Is4() {
		return nil, fmt.Errorf("перехват программ: нужны адреса IPv4 (%v → %v)", opts.From, opts.To)
	}

	s := &Session{opts: opts, stop: make(chan struct{})}
	s.programs.Store(&programs)
	s.steer = New(opts.Config, systemOwner, s.match)

	var err error
	if s.out, err = open(outboundFilter(opts)); err != nil {
		return nil, err
	}
	if s.in, err = open(inboundFilter(opts)); err != nil {
		s.out.close()
		return nil, err
	}

	s.wg.Add(3)
	go s.loop(s.out, s.outbound)
	go s.loop(s.in, s.inbound)
	go s.sweep()

	log.Printf("Перехват программ включён: %s → %s", opts.From, opts.To)
	return s, nil
}

// SetPrograms меняет набор программ без перезапуска перехвата. Уже
// уведённые соединения доживают как есть; решение принимается для новых.
func (s *Session) SetPrograms(programs Programs) {
	s.programs.Store(&programs)
}

// Options — с какими параметрами работает перехват.
func (s *Session) Options() Options { return s.opts }

// Stop выключает перехват и ждёт завершения.
func (s *Session) Stop() {
	close(s.stop)
	s.out.close()
	s.in.close()
	s.wg.Wait()
	log.Printf("Перехват программ выключен")
}

func (s *Session) loop(h handle, handle func([]byte, *address)) {
	defer s.wg.Done()
	buf := make([]byte, mtuMax)
	for {
		var addr address
		n, err := h.recv(buf, &addr)
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
			}
			if isClosed(err) {
				return
			}
			log.Printf("Перехват: %v", err)
			continue
		}
		handle(buf[:n], &addr)
	}
}

func (s *Session) outbound(p []byte, addr *address) {
	if s.fromIf.Load() == nil {
		learned := *addr
		s.fromIf.Store(&learned)
	}
	switch s.steer.Outbound(p) {
	case Drop:
		return
	case Rewritten:
		addr.checksumsValid()
	}
	if err := s.out.send(p, addr); err != nil {
		log.Printf("Перехват: пакет не отправлен: %v", err)
	}
}

func (s *Session) inbound(p []byte, addr *address) {
	if s.steer.Inbound(p) == Rewritten {
		addr.checksumsValid()
		// Строгая модель хоста: пакет на адрес From примется только с его
		// интерфейса.
		if from := s.fromIf.Load(); from != nil {
			addr.IfIdx, addr.SubIfIdx = from.IfIdx, from.SubIfIdx
		}
	}
	if err := s.in.send(p, addr); err != nil {
		log.Printf("Перехват: пакет не доставлен: %v", err)
	}
}

func (s *Session) sweep() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.steer.Sweep()
		}
	}
}

// match — подходит ли процесс (или его предок: игру запускает лаунчер)
// под набор программ.
func (s *Session) match(proto uint8, pid uint32) bool {
	programs := s.programs.Load()
	names := programs.All
	if proto == protoUDP {
		names = append(append([]string(nil), names...), programs.UDP...)
	}
	if len(names) == 0 {
		return false
	}

	for depth := 0; depth < maxAncestors && pid != 0; depth++ {
		p, ok := s.procs.get(pid)
		if !ok {
			return false
		}
		if p.Path != "" && apps.MatchImage(p.Path, names) != "" {
			return true
		}
		pid = p.PPID
	}
	return false
}

// maxAncestors — сколько родителей проверять. Лаунчер → игра — обычно один
// шаг, иногда два-три (Steam → обёртка → игра).
const maxAncestors = 4

// procCache — снимок процессов. Снимок дорогой (миллисекунды), а нужен он
// только на новые соединения. Обновляется, когда процесса в нём нет или
// снимок старше maxAge (номера процессов переиспользуются), но не чаще раза
// в refreshEvery.
type procCache struct {
	mu    sync.Mutex
	procs map[uint32]apps.WinProc
	taken time.Time
}

const (
	refreshEvery = 200 * time.Millisecond
	maxAge       = 2 * time.Second
)

func (c *procCache) get(pid uint32) (apps.WinProc, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.procs[pid]
	age := time.Since(c.taken)
	if (!ok || age > maxAge) && age > refreshEvery {
		c.procs, c.taken = apps.Processes(), time.Now()
		p, ok = c.procs[pid]
	}
	return p, ok
}
