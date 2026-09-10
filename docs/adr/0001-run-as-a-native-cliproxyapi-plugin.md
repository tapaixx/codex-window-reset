# Run as a native CLIProxyAPI plugin

Codex Window Reset will be delivered as a native Go C-shared CLIProxyAPI plugin, based on the architecture of `codex-health-monitor`, rather than as a standalone sidecar or a core package with dual entry points. This keeps installation and account access aligned with CLIProxyAPI and minimizes first-release operational complexity, accepting tighter coupling to the plugin ABI and lifecycle.
