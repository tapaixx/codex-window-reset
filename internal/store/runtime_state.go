package store

import (
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type RuntimeStateRepository struct {
	mu   sync.Mutex
	path string
}

func NewRuntimeStateRepository(dir string) *RuntimeStateRepository {
	return &RuntimeStateRepository{path: filepath.Join(dir, "runtime-state.json")}
}

func emptyRuntimeState() domain.RuntimeState {
	return domain.RuntimeState{
		SchemaVersion:  persistedSchemaVersion,
		Occurrences:    map[string]domain.OccurrenceState{},
		NextRuns:       map[string]time.Time{},
		GuardrailHolds: map[string]domain.GuardrailHold{},
	}
}

func normalizeRuntimeState(state domain.RuntimeState) domain.RuntimeState {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = persistedSchemaVersion
	}
	if state.Occurrences == nil {
		state.Occurrences = map[string]domain.OccurrenceState{}
	}
	if state.NextRuns == nil {
		state.NextRuns = map[string]time.Time{}
	}
	if state.GuardrailHolds == nil {
		state.GuardrailHolds = map[string]domain.GuardrailHold{}
	}
	return state
}

func (r *RuntimeStateRepository) Load() (domain.RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *RuntimeStateRepository) loadLocked() (domain.RuntimeState, error) {
	var state domain.RuntimeState
	err := readJSON(r.path, &state)
	if isMissing(err) {
		return emptyRuntimeState(), nil
	}
	if err != nil {
		return domain.RuntimeState{}, err
	}
	if state.SchemaVersion != persistedSchemaVersion {
		return domain.RuntimeState{}, unsupportedSchema(r.path, state.SchemaVersion)
	}
	return normalizeRuntimeState(state), nil
}

func (r *RuntimeStateRepository) Save(state domain.RuntimeState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked(state)
}

func (r *RuntimeStateRepository) saveLocked(state domain.RuntimeState) error {
	state = normalizeRuntimeState(state)
	if state.SchemaVersion != persistedSchemaVersion {
		return unsupportedSchema(r.path, state.SchemaVersion)
	}
	return writeAtomic(r.path, state)
}

func (r *RuntimeStateRepository) Update(mutate func(*domain.RuntimeState) error) error {
	if mutate == nil {
		return errors.New("runtime state mutation must not be nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.loadLocked()
	if err != nil {
		return err
	}
	if err := mutate(&state); err != nil {
		return err
	}
	return r.saveLocked(state)
}
