# Keep schedule optimization advisory

The scheduler executes only an Operator-approved Preheat Schedule and never changes that schedule from observed behavior on its own. The first release provides a deterministic strategy simulator but does not generate historical Schedule Recommendations because event-driven snapshots cannot reliably reconstruct interactive usage. A future recommendation feature may propose changes only when evidence is sufficient, and applying one will still require explicit Operator approval.
