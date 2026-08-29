package vpn

import (
	"testing"
	"time"
)

// Ответ IpcGet с двумя пирами: счётчики должны сложиться, а время
// рукопожатия — взяться самое свежее.
func TestParseDeviceStatsTwoPeers(t *testing.T) {
	state := `private_key=a8dc000000000000000000000000000000000000000000000000000000000001
listen_port=51820
public_key=b1cd000000000000000000000000000000000000000000000000000000000002
endpoint=203.0.113.7:51820
last_handshake_time_sec=1700000000
last_handshake_time_nsec=500000000
tx_bytes=1200
rx_bytes=3400
persistent_keepalive_interval=25
public_key=c2de000000000000000000000000000000000000000000000000000000000003
endpoint=10.0.0.1:51820
last_handshake_time_sec=1700000900
last_handshake_time_nsec=0
tx_bytes=55
rx_bytes=66
errno=0
`

	got := parseDeviceStats(state)

	if got.TxBytes != 1255 {
		t.Errorf("tx: ожидалось 1255, получено %d", got.TxBytes)
	}
	if got.RxBytes != 3466 {
		t.Errorf("rx: ожидалось 3466, получено %d", got.RxBytes)
	}
	if want := time.Unix(1700000900, 0); !got.LastHandshake.Equal(want) {
		t.Errorf("рукопожатие: ожидалось %v, получено %v", want, got.LastHandshake)
	}
}

// Пир есть, но рукопожатия ещё не было — время должно остаться нулевым,
// иначе watchDevice решит, что соединение установлено.
func TestParseDeviceStatsNoHandshake(t *testing.T) {
	state := `private_key=a8dc000000000000000000000000000000000000000000000000000000000001
public_key=b1cd000000000000000000000000000000000000000000000000000000000002
endpoint=203.0.113.7:51820
last_handshake_time_sec=0
last_handshake_time_nsec=0
tx_bytes=382
rx_bytes=0
errno=0
`

	got := parseDeviceStats(state)

	if !got.LastHandshake.IsZero() {
		t.Errorf("рукопожатия не было, а время не нулевое: %v", got.LastHandshake)
	}
	if got.TxBytes != 382 || got.RxBytes != 0 {
		t.Errorf("счётчики: ожидалось tx=382 rx=0, получено tx=%d rx=%d", got.TxBytes, got.RxBytes)
	}
}

// Наносекунды должны попадать в результат: без них два рукопожатия внутри
// одной секунды неразличимы.
func TestParseDeviceStatsNanoseconds(t *testing.T) {
	state := "public_key=b1cd\nlast_handshake_time_sec=1700000000\nlast_handshake_time_nsec=250000000\n"

	got := parseDeviceStats(state)

	if want := time.Unix(1700000000, 250000000); !got.LastHandshake.Equal(want) {
		t.Errorf("ожидалось %v, получено %v", want, got.LastHandshake)
	}
}

func TestParseDeviceStatsGarbage(t *testing.T) {
	for _, state := range []string{"", "errno=0\n", "мусор без знака равенства\n", "tx_bytes=не число\n"} {
		got := parseDeviceStats(state)
		if got.TxBytes != 0 || got.RxBytes != 0 || !got.LastHandshake.IsZero() {
			t.Errorf("на входе %q ожидался пустой результат, получено %+v", state, got)
		}
	}
}
