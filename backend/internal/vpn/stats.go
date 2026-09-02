package vpn

// Счётчики туннеля: разбор ответа ядра на запрос состояния по UAPI.

import (
	"strconv"
	"strings"
	"time"
)

// deviceStats — сводка по всем пирам устройства.
type deviceStats struct {
	RxBytes       uint64
	TxBytes       uint64
	LastHandshake time.Time
}

// parseDeviceStats разбирает ответ IpcGet. Формат построчный, "ключ=значение";
// счётчики пиров суммируются, из времён рукопожатия берётся самое свежее.
func parseDeviceStats(state string) deviceStats {
	var stats deviceStats
	var sec, nsec int64

	flushHandshake := func() {
		if sec == 0 && nsec == 0 {
			return
		}
		t := time.Unix(sec, nsec)
		if t.After(stats.LastHandshake) {
			stats.LastHandshake = t
		}
		sec, nsec = 0, 0
	}

	for _, line := range strings.Split(state, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}

		switch key {
		case "public_key":
			// Началось описание следующего пира — закрываем предыдущего.
			flushHandshake()
		case "rx_bytes":
			if n, err := strconv.ParseUint(value, 10, 64); err == nil {
				stats.RxBytes += n
			}
		case "tx_bytes":
			if n, err := strconv.ParseUint(value, 10, 64); err == nil {
				stats.TxBytes += n
			}
		case "last_handshake_time_sec":
			sec, _ = strconv.ParseInt(value, 10, 64)
		case "last_handshake_time_nsec":
			nsec, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	flushHandshake()

	return stats
}
