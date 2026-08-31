package dnsproxy

// DNS поверх TCP.
//
// Резолвер идёт сюда, когда ответ не поместился в датаграмму: сервер ставит в
// таком ответе флаг усечения, а клиент по правилам повторяет тот же вопрос по
// TCP. Пока посредник слушал только UDP, повтор упирался в закрытый порт, и
// имя не разрешалось вовсе — при живом, отвечающем UDP-сокете.
//
// Формат отличается от UDP одним: перед каждым сообщением идут два байта его
// длины (RFC 1035, 4.2.2). Из-за этого же по одному соединению можно задать
// несколько вопросов подряд, и резолверы этим пользуются.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"time"
)

const (
	// tcpIdleTimeout — сколько держим соединение без новых вопросов.
	// Резолверы переиспользуют его для следующих запросов, но вечно занимать
	// сокет из-за ушедшего клиента незачем.
	tcpIdleTimeout = 30 * time.Second

	// maxTCPMessage — предел, заданный самим форматом: длина сообщения
	// занимает два байта.
	maxTCPMessage = 65535
)

// serveTCP принимает соединения до закрытия слушателя.
func (p *Proxy) serveTCP(listener *net.TCPListener) {
	defer p.wg.Done()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if p.stopped() {
				return // штатное закрытие
			}
			// Временную ошибку пережидаем: закрытый слушатель вернул нас выше.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}

		p.wg.Add(1)
		go p.guard(func() { p.handleTCP(conn) })
	}
}

// handleTCP обслуживает одно соединение, пока клиент задаёт вопросы.
func (p *Proxy) handleTCP(conn net.Conn) {
	defer conn.Close()

	for {
		if err := conn.SetDeadline(time.Now().Add(tcpIdleTimeout)); err != nil {
			return
		}

		query, err := readMessage(conn)
		if err != nil {
			// Закрытое клиентом соединение и истёкшее ожидание — обычный
			// конец разговора, а не отказ.
			return
		}

		response, err := exchangeTCP(p.servers(), query)
		if err != nil {
			log.Printf("dnsproxy: запрос по TCP не обслужен: %v", err)
			if refusal, ok := servfail(query); ok {
				writeMessage(conn, refusal)
			}
			return
		}

		if err := writeMessage(conn, response); err != nil {
			log.Printf("dnsproxy: не удалось ответить клиенту по TCP: %v", err)
			return
		}

		p.observeAnswer(response)
	}
}

// exchangeTCP пересылает запрос серверам по очереди и возвращает первый ответ.
func exchangeTCP(upstream []netip.AddrPort, query []byte) ([]byte, error) {
	var lastErr error

	for _, server := range upstream {
		conn, err := net.DialTimeout("tcp", server.String(), queryTimeout)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", server, err)
			continue
		}

		response, err := roundTripTCP(conn, query)
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

func roundTripTCP(conn net.Conn, query []byte) ([]byte, error) {
	if err := conn.SetDeadline(time.Now().Add(queryTimeout)); err != nil {
		return nil, err
	}
	if err := writeMessage(conn, query); err != nil {
		return nil, err
	}
	return readMessage(conn)
}

// readMessage читает одно сообщение вместе с его двухбайтовой длиной.
func readMessage(conn net.Conn) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint16(header[:])
	if length == 0 {
		return nil, errors.New("сообщение нулевой длины")
	}

	// Длина пришла из сети, но больше 65535 она быть не может по самому
	// формату — выделять столько безопасно и без дополнительной проверки.
	msg := make([]byte, length)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return nil, err
	}

	return msg, nil
}

// writeMessage отправляет сообщение с его длиной.
func writeMessage(conn net.Conn, msg []byte) error {
	if len(msg) > maxTCPMessage {
		return fmt.Errorf("сообщение длиной %d не помещается в формат", len(msg))
	}

	// Длина и тело уходят одной записью: раздельная отправка заставляет
	// читателя на той стороне ждать второй пакет, а на каждом запросе это
	// лишний оборот по сети.
	framed := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(framed[:2], uint16(len(msg)))
	copy(framed[2:], msg)

	_, err := conn.Write(framed)
	return err
}
