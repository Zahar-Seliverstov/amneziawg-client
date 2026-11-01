//go:build !linux && !windows

package vpn

// Здесь правил для программ нет.
func newAppRouter(policyRouter) appRouter { return nil }
