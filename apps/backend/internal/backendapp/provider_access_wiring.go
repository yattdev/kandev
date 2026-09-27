package backendapp

// configureProviderAccessHost installs grant and teardown authority without
// connecting the credential-bearing plugin RPC. Export remains unavailable.
func configureProviderAccessHost(services *Services) {
	if services == nil || services.ProviderAccess == nil || services.Task == nil ||
		services.GitHub == nil || services.Plugins == nil || services.AgentConversations == nil {
		return
	}
	grants := &providerGrantAuthority{
		tasks: services.Task, plugins: services.Plugins, connections: services.GitHub,
	}
	host := &providerHostAccess{
		store: services.ProviderAccess, grants: grants, managed: services.AgentConversations,
		provider: services.GitHub, tokens: newRegistrationRerunTokens(services.GitHub),
	}
	services.ProviderAccessHost = host
	services.Task.SetProviderAccessCleanup(host)
	services.Task.SetProviderAccessSessionRevoker(host)
	services.AgentConversations.SetProviderAccessSessionRevoker(host)
	services.GitHub.SetProviderAccessConnectionRevoker(host)
	services.Plugins.SetProviderAccessLifecycle(host)
}
