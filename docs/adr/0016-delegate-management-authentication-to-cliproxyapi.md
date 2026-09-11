# Delegate management authentication to CLIProxyAPI

The plugin panel reuses CLIProxyAPI's existing management authentication state and never prompts for or persists a management key under plugin-owned storage. Browser code may consume the host-owned value transiently to authorize a Management API request, but it does not write a plugin-specific credential entry. If the value is absent or rejected, the request surfaces a sanitized Management API error; the plugin does not add a login overlay, session-storage fallback, or second credential lifecycle.
