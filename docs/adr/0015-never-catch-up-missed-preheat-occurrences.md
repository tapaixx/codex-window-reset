# Never catch up missed preheat occurrences

When the plugin is stopped or unavailable until a Preheat Window has begun or elapsed, every unexecuted planned account occurrence is recorded as missed and is not run after restart, even if part of the original window remains. Predictable non-consumption is preferred over catch-up behavior whose timing changed while the Operator could not observe it; the next normal occurrence remains eligible.
