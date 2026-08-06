package config

// SetMCPConfigInMemory registers an MCP server config in memory without
// persisting to disk. It is used for session-declared servers (e.g. MCP
// servers an ACP client passes in session requests); the entry lives for
// the lifetime of the config store and is picked up by MCP
// reinitialization (see internal/agent/tools/mcp.Reinitialize).
func (s *ConfigStore) SetMCPConfigInMemory(name string, cfg MCPConfig) {
	s.mutateInMemory(func(c *Config) {
		if c.MCP == nil {
			c.MCP = make(map[string]MCPConfig)
		}
		c.MCP[name] = cfg
	})
}

// RemoveMCPConfigInMemory removes an in-memory MCP server config entry.
func (s *ConfigStore) RemoveMCPConfigInMemory(name string) {
	s.mutateInMemory(func(c *Config) {
		delete(c.MCP, name)
	})
}
