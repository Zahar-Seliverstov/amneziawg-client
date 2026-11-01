// Package awggo — ядро AmneziaWG на библиотеке amneziawg-go, внутри процесса
// службы.
//
// Отдельного бинарника, unix-сокета и разбора чужого лога нет — вместе с ними
// ушли и их отказы (протухший сокет, «интерфейс занят», гонка при старте).
package awggo

import (
	"fmt"
	"log"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/user/amnezia-web-client/internal/protocol/amneziawg"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// Engine реализует tunnel.Engine.
type Engine struct{}

var _ tunnel.Engine = Engine{}

func (Engine) ID() string { return "amneziawg-go" }

func (Engine) Supports(p tunnel.Config) bool {
	_, ok := p.(*amneziawg.Config)
	return ok
}

// Start создаёт TUN и запускает на нём устройство.
//
// Устройство создаётся заново на каждую попытку, а не переиспользуется:
// сокет ядра привязан к маршруту, который был при его создании, и после смены
// сети (Wi-Fi на кабель) продолжал бы слать пакеты в исчезнувший путь.
func (Engine) Start(p tunnel.Config, opts tunnel.Options) (tunnel.Device, error) {
	cfg, ok := p.(*amneziawg.Config)
	if !ok {
		return nil, fmt.Errorf("%w: конфигурация %q не для amneziawg-go", tunnel.ErrRejected, p.Protocol())
	}

	uapi, err := buildUAPI(cfg, opts.FirewallMark)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", tunnel.ErrRejected, err)
	}

	mtu := cfg.Interface.MTU
	if mtu <= 0 {
		mtu = device.DefaultMTU
	}

	tunDev, err := tun.CreateTUN(opts.Interface, mtu)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать интерфейс %s: %w", opts.Interface, err)
	}

	// Ядро ОС вправе выдать имя, отличное от запрошенного.
	name := opts.Interface
	if actual, err := tunDev.Name(); err == nil && actual != "" {
		name = actual
	}

	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), newLogger(opts.Logf))
	d := &Device{dev: dev, name: name}

	if err := dev.IpcSet(uapi); err != nil {
		d.Close()
		// Повторять нечего: ядро отвергает не сеть, а сами параметры.
		return nil, fmt.Errorf("%w: %v", tunnel.ErrRejected, err)
	}
	if err := dev.Up(); err != nil {
		d.Close()
		return nil, fmt.Errorf("не удалось поднять туннель: %w", err)
	}
	return d, nil
}

// newLogger направляет журнал ядра в журнал службы. Штатный device.NewLogger
// пишет в stdout мимо log — строки шли без префикса и вперемешку по времени.
func newLogger(logf func(string, ...any)) *device.Logger {
	if logf == nil {
		logf = log.Printf
	}
	return &device.Logger{
		Verbosef: func(format string, args ...any) { logf("[awg] "+format, args...) },
		Errorf:   func(format string, args ...any) { logf("[awg] ERROR "+format, args...) },
	}
}

// Device реализует tunnel.Device.
type Device struct {
	dev  *device.Device
	name string
}

func (d *Device) Name() string { return d.name }

func (d *Device) Done() <-chan struct{} { return d.dev.Wait() }

// Close закрывает устройство; TUN исчезает из системы вместе с ним.
func (d *Device) Close() error {
	d.dev.Close()
	return nil
}

func (d *Device) Stats() (tunnel.Stats, error) {
	state, err := d.dev.IpcGet()
	if err != nil {
		return tunnel.Stats{}, err
	}
	return parseStats(state), nil
}
