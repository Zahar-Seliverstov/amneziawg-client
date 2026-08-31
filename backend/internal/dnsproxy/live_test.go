package dnsproxy_test

// Сетевой тест: проверяет посредника на настоящем сервере имён. Требует
// доступа в интернет, поэтому пропускается в коротком режиме (go test -short)
// и не должен попадать в обязательные проверки CI.

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/dnsproxy"
	"github.com/user/amnezia-web-client/internal/routing"
)

func TestLiveResolveMatchesRules(t *testing.T) {
	if testing.Short() {
		t.Skip("сетевой тест")
	}

	matcher := routing.NewMatcher([]config.RoutingRule{
		{Type: config.RuleTypeZone, Value: ".ru", Enabled: true},
		{Type: config.RuleTypeDomain, Value: "example.com", Enabled: true},
	})

	type observation struct {
		name    string
		matched bool
		addrs   int
	}
	seen := make(chan observation, 32)

	proxy := dnsproxy.New(func(a dnsproxy.Answer) {
		seen <- observation{name: a.Name, matched: matcher.Match(a.Name), addrs: len(a.Addrs)}
	})

	listen := netip.MustParseAddrPort("127.0.0.1:0")
	upstream := []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:53")}

	if err := proxy.Start(listen, upstream); err != nil {
		t.Fatalf("посредник не поднялся: %v", err)
	}
	defer proxy.Stop()

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", proxy.Addr().String())
		},
	}

	for _, name := range []string{"yandex.ru", "example.com", "google.com"} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		addrs, err := resolver.LookupHost(ctx, name)
		cancel()

		if err != nil {
			t.Fatalf("резолв %s через посредника не удался: %v", name, err)
		}
		if len(addrs) == 0 {
			t.Fatalf("резолв %s вернул пустой список", name)
		}
		t.Logf("%s -> %v", name, addrs)
	}

	// Сверяем вердикты: под правила должны попасть первые два имени и не
	// попасть google.com.
	deadline := time.After(3 * time.Second)
	verdicts := map[string]bool{}

	for len(verdicts) < 3 {
		select {
		case o := <-seen:
			if o.addrs == 0 {
				t.Errorf("%s: посредник сообщил об ответе без адресов", o.name)
			}
			verdicts[o.name] = o.matched
		case <-deadline:
			t.Fatalf("дождались только %v", verdicts)
		}
	}

	for name, want := range map[string]bool{"yandex.ru": true, "example.com": true, "google.com": false} {
		if got, ok := verdicts[name]; !ok {
			t.Errorf("%s: посредник его не заметил", name)
		} else if got != want {
			t.Errorf("%s: совпадение %v, ждали %v", name, got, want)
		}
	}
}
