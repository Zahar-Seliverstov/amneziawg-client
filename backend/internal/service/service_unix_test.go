//go:build !windows

package service

import (
	"strings"
	"testing"
)

func TestUnitFile(t *testing.T) {
	unit := unitFile(Spec{
		Name:         "awg-ui",
		Description:  "AWG Client UI",
		Args:         []string{"-www", "/opt/awg client/www"},
		Unprivileged: true,
		After:        []string{"awg-service"},
	}, "/usr/lib/awg-client/awg-ui")

	for _, want := range []string{
		`ExecStart=/usr/lib/awg-client/awg-ui -www "/opt/awg client/www"`,
		"After=network-online.target awg-service.service",
		"DynamicUser=yes",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("в юните нет %q:\n%s", want, unit)
		}
	}
}

func TestUnitFilePrivileged(t *testing.T) {
	unit := unitFile(Spec{Name: "awg-service"}, "/usr/lib/awg-client/awg-service")
	if strings.Contains(unit, "DynamicUser") {
		t.Errorf("служба с правами не должна получать временного пользователя:\n%s", unit)
	}
}
