package vpn

// На Linux правила для программ — часть маршрутизации по политикам: метка
// программы ведёт в её таблицу (policy_linux.go).
func newAppRouter(policy policyRouter) appRouter {
	if router, ok := policy.(appRouter); ok {
		return router
	}
	return nil
}
