# Infer recommendations from quota snapshots

The current CLIProxyAPI plugin ABI does not expose a complete stream of user requests. If Schedule Recommendations are introduced after the first release, they will be inferred from Usage Snapshots with an explicit confidence rating rather than presented as exact observations; insufficient or operation-biased data produces no recommendation.
