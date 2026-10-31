package geodata

// Чтение формата protobuf — ровно то подмножество, которое нужно файлам
// geoip.dat и geosite.dat.
//
// Своё, а не google.golang.org/protobuf: схема этих файлов — пять сообщений
// по два-три поля, и ради них тянуть генератор и рантайм protobuf незачем.
// Формат провода при этом стабилен с 2008 года.

import (
	"bufio"
	"errors"
	"io"
)

// Типы значений на проводе.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

var errMalformed = errors.New("файл повреждён или не в формате v2ray")

// buf — разбор сообщения, целиком лежащего в памяти.
type buf struct {
	data []byte
	pos  int
}

func (b *buf) done() bool { return b.pos >= len(b.data) }

func (b *buf) varint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if b.pos >= len(b.data) {
			return 0, errMalformed
		}
		c := b.data[b.pos]
		b.pos++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, errMalformed
}

// field читает заголовок поля: номер и тип значения.
func (b *buf) field() (num int, wire int, err error) {
	key, err := b.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(key >> 3), int(key & 7), nil
}

// bytes читает значение с длиной. Возвращает срез исходных данных, без копии.
func (b *buf) bytes() ([]byte, error) {
	n, err := b.varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(b.data)-b.pos) {
		return nil, errMalformed
	}
	out := b.data[b.pos : b.pos+int(n)]
	b.pos += int(n)
	return out, nil
}

// skip пропускает значение незнакомого поля.
func (b *buf) skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := b.varint()
		return err
	case wireBytes:
		_, err := b.bytes()
		return err
	case wireFixed32:
		return b.advance(4)
	case wireFixed64:
		return b.advance(8)
	}
	return errMalformed
}

func (b *buf) advance(n int) error {
	if n > len(b.data)-b.pos {
		return errMalformed
	}
	b.pos += n
	return nil
}

// stream — разбор файла потоком. geosite.dat бывает за полсотни мегабайт, а
// для оглавления из него нужны только имена категорий: держать файл в памяти
// ради них незачем.
type stream struct {
	r   *bufio.Reader
	pos int64
}

func (s *stream) varint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		c, err := s.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && shift > 0 {
				return 0, errMalformed
			}
			return 0, err
		}
		s.pos++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, errMalformed
}

func (s *stream) field() (num int, wire int, err error) {
	key, err := s.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(key >> 3), int(key & 7), nil
}

func (s *stream) discard(n int64) error {
	done, err := s.r.Discard(int(n))
	s.pos += int64(done)
	if err != nil {
		return errMalformed
	}
	return nil
}

func (s *stream) read(n int64) ([]byte, error) {
	out := make([]byte, n)
	done, err := io.ReadFull(s.r, out)
	s.pos += int64(done)
	if err != nil {
		return nil, errMalformed
	}
	return out, nil
}

func (s *stream) skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := s.varint()
		return err
	case wireBytes:
		n, err := s.varint()
		if err != nil {
			return err
		}
		return s.discard(int64(n))
	case wireFixed32:
		return s.discard(4)
	case wireFixed64:
		return s.discard(8)
	}
	return errMalformed
}
