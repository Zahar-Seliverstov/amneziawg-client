package awggo

// UAPI — построчный текстовый протокол «ключ=значение», которым с ядром
// WireGuard (и amneziawg-go) говорит и сам wg(8).

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/user/amnezia-web-client/internal/protocol/amneziawg"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// buildUAPI переводит конфигурацию в текст для IpcSet. mark — метка
// собственных пакетов туннеля, 0 — без неё.
func buildUAPI(cfg *amneziawg.Config, mark uint32) (string, error) {
	var b uapiWriter

	b.key("private_key", cfg.Interface.PrivateKey)
	if mark != 0 {
		b.set("fwmark", strconv.FormatUint(uint64(mark), 10))
	}
	for _, p := range cfg.Interface.Obfuscation {
		if p.Kind == amneziawg.KindKey {
			b.key(p.Key, p.Value)
		} else {
			b.set(p.Key, p.Value)
		}
	}

	for _, peer := range cfg.Peers {
		b.key("public_key", peer.PublicKey)
		if peer.PresharedKey != "" {
			b.key("preshared_key", peer.PresharedKey)
		}
		if peer.Endpoint != "" {
			b.set("endpoint", peer.Endpoint)
		}
		if peer.Keepalive != "" {
			b.set("persistent_keepalive_interval", peer.Keepalive)
		}
		for _, prefix := range peer.AllowedIPs {
			b.set("allowed_ip", prefix)
		}
	}

	return b.String(), b.err
}

// uapiWriter копит строки и первую ошибку — чтобы не проверять её на каждой.
type uapiWriter struct {
	strings.Builder
	err error
}

func (w *uapiWriter) set(key, value string) {
	fmt.Fprintf(w, "%s=%s\n", key, value)
}

// key пишет ключ: в конфигурации он в base64, ядру нужен hex.
func (w *uapiWriter) key(name, b64 string) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		if w.err == nil {
			w.err = fmt.Errorf("%s: неверный ключ", name)
		}
		return
	}
	w.set(name, hex.EncodeToString(raw))
}

// parseStats разбирает ответ IpcGet: счётчики пиров суммируются, из времён
// рукопожатия берётся самое свежее.
func parseStats(state string) tunnel.Stats {
	var (
		stats     tunnel.Stats
		sec, nsec int64
	)

	flush := func() {
		if sec != 0 || nsec != 0 {
			if t := time.Unix(sec, nsec); t.After(stats.LastHandshake) {
				stats.LastHandshake = t
			}
		}
		sec, nsec = 0, 0
	}

	for _, line := range strings.Split(state, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "public_key": // начался следующий пир
			flush()
		case "rx_bytes":
			n, _ := strconv.ParseUint(value, 10, 64)
			stats.RxBytes += n
		case "tx_bytes":
			n, _ := strconv.ParseUint(value, 10, 64)
			stats.TxBytes += n
		case "last_handshake_time_sec":
			sec, _ = strconv.ParseInt(value, 10, 64)
		case "last_handshake_time_nsec":
			nsec, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	flush()

	return stats
}
