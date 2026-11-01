package appsteer

// Чей это порт: таблицы соединений Windows с номерами процессов.
//
// Спрашиваем в момент первого пакета соединения. SYN к этому моменту уже
// отправлен в стек, и соединение в состоянии SYN_SENT в таблице есть; у
// UDP сокет привязан к порту до первой отправки.

import (
	"encoding/binary"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi           = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcp = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdp = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	udpTableOwnerPID    = 1 // UDP_TABLE_OWNER_PID

	// Размеры строк MIB_*ROW_OWNER_PID.
	tcp4Row = 24 // state, localAddr, localPort, remoteAddr, remotePort, pid
	tcp6Row = 56 // localAddr[16], scope, localPort, remoteAddr[16], scope, remotePort, state, pid
	udp4Row = 12 // localAddr, localPort, pid
	udp6Row = 28 // localAddr[16], scope, localPort, pid
)

// systemOwner — OwnerFunc на таблицах Windows.
func systemOwner(proto uint8, local netip.AddrPort) (uint32, bool) {
	v6 := local.Addr().Is6() && !local.Addr().Is4In6()
	table, err := ownerTable(proto, v6)
	if err != nil || len(table) < 4 {
		return 0, false
	}

	count := int(binary.LittleEndian.Uint32(table[0:4]))
	rows := table[4:]

	// Порт в строке — в сетевом порядке в младших двух байтах DWORD.
	port := func(dw []byte) uint16 { return binary.BigEndian.Uint16(dw[0:2]) }

	var size, addrOff, portOff, pidOff, addrLen int
	switch {
	case proto == protoTCP && !v6:
		size, addrOff, portOff, pidOff, addrLen = tcp4Row, 4, 8, 20, 4
	case proto == protoTCP && v6:
		size, addrOff, portOff, pidOff, addrLen = tcp6Row, 0, 20, 52, 16
	case proto == protoUDP && !v6:
		size, addrOff, portOff, pidOff, addrLen = udp4Row, 0, 4, 8, 4
	default:
		size, addrOff, portOff, pidOff, addrLen = udp6Row, 0, 20, 24, 16
	}

	want := local.Addr().Unmap()
	var wildcard uint32
	for i := 0; i < count && (i+1)*size <= len(rows); i++ {
		row := rows[i*size : (i+1)*size]
		if port(row[portOff:]) != local.Port() {
			continue
		}
		pid := binary.LittleEndian.Uint32(row[pidOff:])
		addr, _ := netip.AddrFromSlice(row[addrOff : addrOff+addrLen])
		if addr.Unmap() == want {
			return pid, true
		}
		// UDP-сокет, привязанный ко всем адресам (0.0.0.0): подходит, если
		// точного совпадения нет.
		if addr.IsUnspecified() {
			wildcard = pid
		}
	}
	return wildcard, wildcard != 0
}

// ownerTable читает таблицу целиком. Размер заранее неизвестен: первый вызов
// его сообщает, а между вызовами таблица могла подрасти — тогда повторяем.
func ownerTable(proto uint8, v6 bool) ([]byte, error) {
	family := uintptr(windows.AF_INET)
	if v6 {
		family = windows.AF_INET6
	}
	proc, class := procGetExtendedTcp, uintptr(tcpTableOwnerPIDAll)
	if proto == protoUDP {
		proc, class = procGetExtendedUdp, udpTableOwnerPID
	}

	size := uint32(16 << 10)
	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]byte, size)
		r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, family, class, 0)
		switch windows.Errno(r) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 4 << 10
			continue
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}
