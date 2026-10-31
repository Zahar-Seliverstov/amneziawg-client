//go:build !linux

package vpn

// Маршрутизации по политикам вне Linux нет: менеджер ставит маршруты в общую
// таблицу, как раньше (см. policy.go).
func newPolicyRouter() policyRouter {
	return nil
}
