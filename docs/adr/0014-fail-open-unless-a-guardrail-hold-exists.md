# Fail open unless a guardrail hold exists

When a planned preheat cannot refresh quota, it proceeds despite unknown current quota unless the last successful snapshot established a Guardrail Hold. A stale low-quota snapshot continues to veto automatic requests until a successful refresh proves recovery, while absence of a hold does not turn a transient quota-endpoint failure into lost work coverage. This deliberately makes unknown quota fail open but makes known long-window exhaustion fail closed.
