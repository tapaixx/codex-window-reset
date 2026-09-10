# Selectively port the reference plugin

Codex Window Reset will selectively port the proven CLIProxyAPI ABI bridge, strict SSE probe parsing, quota protocol parsing, atomic persistence, tests, and release mechanics from `tapaixx/codex-health-monitor` while redesigning the application boundaries and panel. Copying the repository wholesale would preserve duplicate schedulers, global runtime registries, browser-side quota calls, and script-order overrides; a clean-room rewrite would discard validated protocol behavior without a compensating benefit.
