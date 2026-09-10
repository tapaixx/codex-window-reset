# Share the schedule and isolate account state

Scheduled Accounts share one Operator-managed Preheat Schedule while each account keeps its own Usage Window state. Accounts are distributed deterministically across each Preheat Window from the local occurrence identity and stable account identity, so restarts reproduce the plan while later occurrences can redistribute load. This avoids per-account calendars without forcing accounts with different quota state to send requests simultaneously or make the same preheating decision.
