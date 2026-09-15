package app

import (
	"sort"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// Upcoming reports the preheat work that currently holds a timer, grouped by
// Preheat Window.
//
// NextRuns is the authority for "armed", not the occurrence map: an occurrence
// without a next run has no timer and will not fire, and that distinction is
// exactly what a restart bug can break invisibly. Only planned occurrences are
// included — a NextRuns entry on a failed occurrence is a compensation retry,
// which is follow-up work rather than a scheduled preheat.
//
// Like Status, runtime state is loaded outside Runtime.mu so repository I/O
// never blocks operation orchestration.
func (r *Runtime) Upcoming() domain.UpcomingView {
	if r == nil {
		return domain.UpcomingView{Batches: []domain.UpcomingBatch{}}
	}
	state, stateErr := r.deps.State.Load()
	if stateErr != nil {
		r.setStoreError(stateErr)
	}

	r.mu.RLock()
	enabled := r.config.Enabled
	storeError := r.storeErrorCode
	r.mu.RUnlock()

	view := domain.UpcomingView{Enabled: enabled, StoreErrorCode: storeError, Batches: []domain.UpcomingBatch{}}
	if stateErr != nil {
		return view
	}

	type batchKey struct {
		date   string
		period int
	}
	grouped := make(map[batchKey]*domain.UpcomingBatch)
	for id, plannedAt := range state.NextRuns {
		occurrence, ok := state.Occurrences[id]
		if !ok || occurrence.Status != domain.OccurrencePlanned {
			continue
		}
		key := batchKey{date: occurrence.LocalDate, period: occurrence.PeriodIndex}
		batch, exists := grouped[key]
		if !exists {
			batch = &domain.UpcomingBatch{
				LocalDate:   occurrence.LocalDate,
				PeriodIndex: occurrence.PeriodIndex,
				WindowStart: occurrence.WindowStart.UTC(),
				WindowEnd:   occurrence.WindowEnd.UTC(),
			}
			grouped[key] = batch
		}
		entry := domain.UpcomingOccurrence{
			AccountKey: normalizeKey(occurrence.AccountKey),
			PlannedAt:  plannedAt.UTC(),
		}
		if _, held := state.GuardrailHolds[entry.AccountKey]; held {
			entry.BlockedReason = domain.BlockedGuardrailHold
		}
		batch.Occurrences = append(batch.Occurrences, entry)
	}

	for _, batch := range grouped {
		sort.Slice(batch.Occurrences, func(i, j int) bool {
			if batch.Occurrences[i].PlannedAt.Equal(batch.Occurrences[j].PlannedAt) {
				return batch.Occurrences[i].AccountKey < batch.Occurrences[j].AccountKey
			}
			return batch.Occurrences[i].PlannedAt.Before(batch.Occurrences[j].PlannedAt)
		})
		view.Batches = append(view.Batches, *batch)
	}
	sort.Slice(view.Batches, func(i, j int) bool {
		left, right := view.Batches[i], view.Batches[j]
		if !left.WindowStart.Equal(right.WindowStart) {
			return left.WindowStart.Before(right.WindowStart)
		}
		if left.LocalDate != right.LocalDate {
			return left.LocalDate < right.LocalDate
		}
		return left.PeriodIndex < right.PeriodIndex
	})

	var next time.Time
	for _, batch := range view.Batches {
		for _, occurrence := range batch.Occurrences {
			if next.IsZero() || occurrence.PlannedAt.Before(next) {
				next = occurrence.PlannedAt
			}
		}
	}
	view.NextRunAt = next
	return view
}
