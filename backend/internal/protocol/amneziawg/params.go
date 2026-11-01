package amneziawg

// Параметры маскировки AmneziaWG — одной таблицей.
//
// Раньше каждый параметр был отдельным полем структуры, отдельной веткой
// разбора и отдельной строкой в тексте для ядра. Новые параметры при этом
// терялись молча: I1–I5 не разбирались вовсе, а H1–H4 диапазоном («100-200»,
// AmneziaWG v2) не проходили разбор числа и пропадали — сервер выбрасывал
// наши пакеты без единого слова в журнале. Теперь параметр — одна строка
// таблицы, а значение, которое не удалось понять, — ошибка разбора.

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Kind — как записывается и проверяется значение параметра.
type Kind int

const (
	// KindNumber — целое без знака.
	KindNumber Kind = iota
	// KindRange — число или диапазон "от-до".
	KindRange
	// KindKey — ключ в base64, 32 байта.
	KindKey
	// KindSwitch — переключатель; хранится как "true"/"false".
	KindSwitch
	// KindChain — цепочка особых пакетов вида "<b 0x…><r 16>"; синтаксис
	// проверяет ядро.
	KindChain
)

// Param — один параметр маскировки.
type Param struct {
	// Key — имя в протоколе настройки ядра (UAPI): "jc", "h1".
	Key   string
	Value string
	Kind  Kind
}

type paramSpec struct {
	key  string
	kind Kind
	bits int // для KindNumber
}

// params — все параметры маскировки, по имени в .conf в нижнем регистре.
var params = map[string]paramSpec{
	"jc":   {key: "jc", kind: KindNumber, bits: 32},
	"jmin": {key: "jmin", kind: KindNumber, bits: 32},
	"jmax": {key: "jmax", kind: KindNumber, bits: 32},
	"s1":   {key: "s1", kind: KindNumber, bits: 16},
	"s2":   {key: "s2", kind: KindNumber, bits: 16},
	"s3":   {key: "s3", kind: KindNumber, bits: 16},
	"s4":   {key: "s4", kind: KindNumber, bits: 16},
	"h1":   {key: "h1", kind: KindRange},
	"h2":   {key: "h2", kind: KindRange},
	"h3":   {key: "h3", kind: KindRange},
	"h4":   {key: "h4", kind: KindRange},
	"i1":   {key: "i1", kind: KindChain},
	"i2":   {key: "i2", kind: KindChain},
	"i3":   {key: "i3", kind: KindChain},
	"i4":   {key: "i4", kind: KindChain},
	"i5":   {key: "i5", kind: KindChain},

	// AmneziaWG v2.
	"headerprotectionkey":    {key: "header_protection_key", kind: KindKey},
	"contentpaddingaddition": {key: "content_padding_addition", kind: KindRange},
	"rekeyaftertime":         {key: "rekey_after_time", kind: KindRange},
	"rekeytimeout":           {key: "rekey_timeout", kind: KindRange},
	"rejectaftertime":        {key: "reject_after_time", kind: KindRange},
	"keepalivetimeout":       {key: "keepalive_timeout", kind: KindRange},
	"maxhandshakeattempts":   {key: "max_handshake_attempts", kind: KindRange},
	"randomtrailers":         {key: "random_trailers", kind: KindSwitch},
	"disablecookies":         {key: "disable_cookies", kind: KindSwitch},
}

// parseParam проверяет значение и приводит его к виду, который понимает
// ядро. name — имя, как оно написано в файле: оно попадает в ошибку.
func parseParam(spec paramSpec, name, value string) (Param, error) {
	p := Param{Key: spec.key, Value: value, Kind: spec.kind}

	switch spec.kind {
	case KindNumber:
		if _, err := strconv.ParseUint(value, 10, spec.bits); err != nil {
			return p, fmt.Errorf("%s = %q: ожидалось целое число", name, value)
		}
	case KindRange:
		if err := checkRange(value); err != nil {
			return p, fmt.Errorf("%s = %q: %w", name, value, err)
		}
	case KindKey:
		if err := checkKey(name, value); err != nil {
			return p, err
		}
	case KindSwitch:
		on, ok := parseSwitch(value)
		if !ok {
			return p, fmt.Errorf("%s = %q: ожидалось on или off", name, value)
		}
		p.Value = strconv.FormatBool(on)
	case KindChain:
		if !strings.Contains(value, "<") {
			return p, fmt.Errorf("%s = %q: ожидалась цепочка вида <b 0x…>", name, value)
		}
	}
	return p, nil
}

// checkRange принимает "N" и "N-M" при N ≤ M.
func checkRange(value string) error {
	lo, hi, isRange := strings.Cut(value, "-")
	if !isRange {
		hi = lo
	}
	from, err1 := strconv.ParseUint(strings.TrimSpace(lo), 10, 32)
	to, err2 := strconv.ParseUint(strings.TrimSpace(hi), 10, 32)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("ожидалось число или диапазон вида 10-100")
	}
	if to < from {
		return fmt.Errorf("начало диапазона больше конца")
	}
	return nil
}

func parseSwitch(value string) (on, ok bool) {
	switch strings.ToLower(value) {
	case "on", "true", "yes", "1":
		return true, true
	case "off", "false", "no", "0":
		return false, true
	}
	return false, false
}

// keyLen — длина ключа WireGuard: 32 байта, они же 44 знака base64.
const keyLen = 32

// checkKey проверяет ключ. Длина важнее формата: строка из 44 знаков base64,
// декодирующаяся не в 32 байта, выглядит как ключ и ключом не является.
func checkKey(field, value string) error {
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
