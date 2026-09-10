# Target a single trusted operator

Each Codex Window Reset installation assumes one trusted Operator with authority over every Codex Account in its CLIProxyAPI instance. The first release will not introduce users, roles, or per-account authorization because the host Management API and plugin lifecycle already form an administrative boundary; deployments needing mutually untrusted administrators must provide that isolation outside the plugin.
