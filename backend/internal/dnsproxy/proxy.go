// Package dnsproxy содержит локального посредника DNS: он встаёт между
// системным резолвером и сервером имён туннеля и сообщает наблюдателю,
// какие адреса вернулись для каждого запрошенного имени.
//
// Это нужно маршрутизации по доменам и зонам. Адрес сайта заранее не известен
// и меняется со временем (CDN отдаёт разные адреса разным клиентам и в разные
// минуты), поэтому единственный достоверный момент, когда его можно узнать, —
// ответ DNS, который получил сам пользователь. Разовый резолв при подключении
// такой гарантии не даёт: он устаревает через минуты.
//
// Посредник намеренно ничего не знает про маршруты. Он только наблюдает и
// отдаёт факты наружу — решение, что с ними делать, принимает вызывающий.
package dnsproxy

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Answer — что удалось узнать из одного ответа DNS.
type Answer struct {
	// Name — запрошенное имя без завершающей точки, в нижнем регистре.
	Name string
	// Addrs — адреса из записей A и AAAA.
	Addrs []netip.Addr
	// TTL — наименьший TTL среди использованных записей.
	TTL time.Duration
}

// Observer вызывается для каждого ответа, в котором нашлись адреса.
// Вызов происходит в отдельной горутине обработки запроса, поэтому
// реализация обязана быть безопасной для конкурентного вызова.
type Observer func(Answer)

const (
	// queryTimeout — сколько ждём вышестоящий сервер. Системный резолвер
	// повторит запрос сам, поэтому ждать долго смысла нет.
	queryTimeout = 3 * time.Second

	// maxMessage — потолок размера сообщения. 4096 — обычный размер буфера
	// EDNS0; ответы крупнее приходят по TCP, который мы не обслуживаем.
	maxMessage = 4096

	// minTTL защищает от эфемерных ответов: маршрут, живущий пару секунд,
	// не успеет принести пользы, зато будет дёргать таблицу маршрутизации.
	minTTL = 60 * time.Second

	// maxTTL ограничивает срок жизни сверху: адрес, который больше никто не
	// спрашивает, не должен висеть в таблице вечно.
	maxTTL = 6 * time.Hour
)

// Proxy — DNS-посредник поверх UDP.
//
// Нулевое значение непригодно, используйте New.
type Proxy struct {
	observe Observer

	mu       sync.Mutex
	started  bool
	conn     *net.UDPConn
	upstream []netip.AddrPort

	wg   sync.WaitGroup
	stop chan struct{}
	once sync.Once
}

// New создаёт посредника. observe может быть nil — тогда посредник работает
// как обычный пересыльщик.
func New(observe Observer) *Proxy {
	return &Proxy{observe: observe, stop: make(chan struct{})}
}

// Start открывает слушающий сокет и начинает обслуживать запросы.
//
// upstream — серверы, которым пересылаются запросы, в порядке предпочтения.
// Пустой список — ошибка: пересылать было бы некуда, и посредник молча
// съедал бы весь DNS.
func (p *Proxy) Start(listen netip.AddrPort, upstream []netip.AddrPort) error {
	if len(upstream) == 0 {
		return errors.New("dnsproxy: не задан ни один вышестоящий сервер")
	}

	// Повторный запуск потерял бы ссылку на прежний сокет: он остался бы
	// открытым навсегда, а Stop закрыл бы только последний.
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return errors.New("dnsproxy: посредник уже запущен")
	}
	p.started = true
	p.mu.Unlock()

	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(listen))
	if err != nil {
		p.mu.Lock()
		p.started = false
		p.mu.Unlock()
		return fmt.Errorf("dnsproxy: не удалось занять %s: %w", listen, err)
	}

	p.mu.Lock()
	p.conn = conn
	p.upstream = append([]netip.AddrPort(nil), upstream...)
	p.mu.Unlock()

	p.wg.Add(1)
	go p.serve(conn)

	return nil
}

// Addr возвращает адрес, на котором посредник фактически слушает. Пригодится,
// когда порт выбрало ядро (запрошен нулевой). До Start возвращает нулевое
// значение.
func (p *Proxy) Addr() netip.AddrPort {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return netip.AddrPort{}
	}
	return p.conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

// Stop закрывает сокет и дожидается завершения обработчиков.
// Повторный вызов безопасен.
func (p *Proxy) Stop() {
	p.once.Do(func() {
		close(p.stop)

		p.mu.Lock()
		conn := p.conn
		p.conn = nil
		p.mu.Unlock()

		if conn != nil {
			conn.Close()
		}
	})

	p.wg.Wait()
}

// serve читает запросы до закрытия сокета.
func (p *Proxy) serve(conn *net.UDPConn) {
	defer p.wg.Done()

	buf := make([]byte, maxMessage)
	for {
		n, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-p.stop:
				return // штатное закрытие
			default:
			}
			// Временную ошибку чтения пережидаем: закрытый сокет нас уже
			// вернул выше, а всё остальное лечится следующей итерацией.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}

		// Копия: буфер переиспользуется следующим чтением.
		query := make([]byte, n)
		copy(query, buf[:n])

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			// Разбор чужих данных не должен ронять весь backend: ответ
			// приходит из сети, и на кривом сообщении паника вероятнее,
			// чем где-либо ещё.
			defer func() {
				if r := recover(); r != nil {
					log.Printf("dnsproxy: паника при обработке запроса: %v", r)
				}
			}()
			p.handle(conn, query, client)
		}()
	}
}

// handle пересылает один запрос и возвращает ответ клиенту.
func (p *Proxy) handle(conn *net.UDPConn, query []byte, client *net.UDPAddr) {
	p.mu.Lock()
	upstream := p.upstream
	p.mu.Unlock()

	response, err := exchange(upstream, query)
	if err != nil {
		log.Printf("dnsproxy: запрос не обслужен: %v", err)
		if refusal, ok := servfail(query); ok {
			conn.WriteToUDP(refusal, client)
		}
		return
	}

	// Клиенту отвечаем в первую очередь: наблюдение — побочная задача,
	// и задерживать из-за неё резолвинг нельзя.
	if _, err := conn.WriteToUDP(response, client); err != nil {
		log.Printf("dnsproxy: не удалось ответить клиенту: %v", err)
	}

	if p.observe == nil {
		return
	}
	if answer, ok := parseAnswer(response); ok {
		p.observe(answer)
	}
}

// exchange пересылает запрос серверам по очереди и возвращает первый ответ.
func exchange(upstream []netip.AddrPort, query []byte) ([]byte, error) {
	var lastErr error

	for _, server := range upstream {
		conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(server))
		if err != nil {
			lastErr = err
			continue
		}

		response, err := roundTrip(conn, query)
		conn.Close()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", server, err)
			continue
		}
		return response, nil
	}

	if lastErr == nil {
		lastErr = errors.New("нет доступных серверов")
	}
	return nil, lastErr
}

// roundTrip выполняет один обмен по уже открытому сокету.
func roundTrip(conn *net.UDPConn, query []byte) ([]byte, error) {
	if err := conn.SetDeadline(time.Now().Add(queryTimeout)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}

	buf := make([]byte, maxMessage)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// parseAnswer достаёт из ответа имя и адреса. Второе значение — нашлось ли
// хоть что-то полезное; ответы без A/AAAA (CNAME без адреса, NXDOMAIN,
// записи MX и прочее) наблюдателя не интересуют.
func parseAnswer(msg []byte) (Answer, bool) {
	var parser dnsmessage.Parser

	if _, err := parser.Start(msg); err != nil {
		return Answer{}, false
	}

	question, err := parser.Question()
	if err != nil {
		return Answer{}, false
	}
	if err := parser.SkipAllQuestions(); err != nil {
		return Answer{}, false
	}

	answer := Answer{Name: normalizeName(question.Name.String())}
	lowest := ^uint32(0)

	for {
		header, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return Answer{}, false
		}

		switch header.Type {
		case dnsmessage.TypeA:
			record, err := parser.AResource()
			if err != nil {
				return Answer{}, false
			}
			answer.Addrs = append(answer.Addrs, netip.AddrFrom4(record.A))

		case dnsmessage.TypeAAAA:
			record, err := parser.AAAAResource()
			if err != nil {
				return Answer{}, false
			}
			answer.Addrs = append(answer.Addrs, netip.AddrFrom16(record.AAAA))

		default:
			// CNAME по пути к адресу — обычное дело, пропускаем молча.
			if err := parser.SkipAnswer(); err != nil {
				return Answer{}, false
			}
			continue
		}

		if header.TTL < lowest {
			lowest = header.TTL
		}
	}

	if len(answer.Addrs) == 0 {
		return Answer{}, false
	}

	answer.TTL = clampTTL(lowest)
	return answer, true
}

// clampTTL приводит TTL из ответа к разумным границам.
func clampTTL(ttl uint32) time.Duration {
	d := time.Duration(ttl) * time.Second
	switch {
	case d < minTTL:
		return minTTL
	case d > maxTTL:
		return maxTTL
	default:
		return d
	}
}

// normalizeName приводит имя из DNS к виду, с которым сравниваются правила.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

// servfail собирает отказ в ответ на запрос: без него системный резолвер
// ждёт таймаута, и любая заминка вышестоящего сервера превращается в
// многосекундное подвисание всего, что резолвит имена.
func servfail(query []byte) ([]byte, bool) {
	var parser dnsmessage.Parser

	header, err := parser.Start(query)
	if err != nil {
		return nil, false
	}
	question, err := parser.Question()
	if err != nil {
		return nil, false
	}

	header.Response = true
	header.RCode = dnsmessage.RCodeServerFailure

	builder := dnsmessage.NewBuilder(nil, header)
	if err := builder.StartQuestions(); err != nil {
		return nil, false
	}
	if err := builder.Question(question); err != nil {
		return nil, false
	}

	msg, err := builder.Finish()
	if err != nil {
		return nil, false
	}
	return msg, true
}
