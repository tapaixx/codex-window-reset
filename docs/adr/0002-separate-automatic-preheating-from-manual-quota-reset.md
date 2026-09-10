# Separate automatic preheating from manual quota reset

Codex Window Reset supports both scheduled Preheat Requests and explicit Quota Resets, but only preheating may run automatically. A Quota Reset consumes a limited Reset Credit, so it must target exactly one Codex Account, be initiated by the Operator through a confirmation dialog that shows the remaining credits and impact, and be recorded in a Reset Audit. The reset operation uses server-side single-flight and an idempotency key to prevent repeated clicks or concurrent requests from consuming the entitlement twice.
