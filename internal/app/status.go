package app

import (
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// Status returns a synchronized, management-safe view of configuration and
// current run progress.  Runtime state is loaded outside Runtime.mu because
// repository I/O must never block or deadlock operation orchestration.
func (r *Runtime) Status() domain.StatusView {
	if r == nil {
		return domain.StatusView{NextRuns: map[string]time.Time{}}
	}
	state, stateErr := r.deps.State.Load()
	if stateErr != nil {
		r.setStoreError(stateErr)
	}

	r.mu.RLock()
	config := r.config
	storeError := r.storeErrorCode
	var runID string
	var total, completed int
	if r.run != nil {
		runID = r.run.id
		total = r.run.total
		completed = r.run.completed
	}
	r.mu.RUnlock()

	nextRuns := make(map[string]time.Time)
	if stateErr == nil {
		for key, instant := range state.NextRuns {
			nextRuns[key] = instant.UTC()
		}
	}
	return domain.StatusView{
		Enabled:            config.Enabled,
		StoreErrorCode:     storeError,
		NextRuns:           nextRuns,
		RunID:              runID,
		RunTotal:           total,
		RunCompleted:       completed,
		GuardrailHoldCount: len(state.GuardrailHolds),
	}
}
