# Persist reset intent before consuming a credit

Before calling the irreversible upstream Quota Reset endpoint, the plugin atomically records a pending Reset Audit keyed by the client idempotency key. Replays return the recorded or pending outcome instead of issuing a new consume request, and a crash-ambiguous operation remains `unknown` until reconciled rather than being reported as success or retried with a new key.
