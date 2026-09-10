# Capture quota snapshots around operations

The plugin does not poll quota merely because the panel is open. It captures Usage Snapshots on manual refresh, immediately before a Health Probe or Preheat Request, and after an operation that can affect quota; snapshots live only in the Runtime, remain visible with their capture time, and become stale after five minutes. This reduces upstream traffic and false “live” semantics at the cost of an empty quota view after restart and data too sparse and operation-biased for first-release Schedule Recommendations.
