package store

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type auditDocument struct {
	SchemaVersion int                 `json:"schema_version"`
	Records       []domain.ResetAudit `json:"records"`
}

type ResetAuditRepository struct {
	mu        sync.Mutex
	path      string
	retention time.Duration
	clock     domain.Clock
}

func NewResetAuditRepository(dir string, retention time.Duration, clock domain.Clock) *ResetAuditRepository {
	return &ResetAuditRepository{
		path:      filepath.Join(dir, "reset-audit.json"),
		retention: retention,
		clock:     clock,
	}
}

func (r *ResetAuditRepository) now() time.Time {
	if r.clock == nil {
		return time.Now().UTC()
	}
	return r.clock.Now().UTC()
}

func (r *ResetAuditRepository) loadLocked() ([]domain.ResetAudit, error) {
	var document auditDocument
	err := readJSON(r.path, &document)
	if isMissing(err) {
		return []domain.ResetAudit{}, nil
	}
	if err != nil {
		return nil, err
	}
	if document.SchemaVersion != persistedSchemaVersion {
		return nil, unsupportedSchema(r.path, document.SchemaVersion)
	}
	if document.Records == nil {
		document.Records = []domain.ResetAudit{}
	}
	if r.retention <= 0 {
		return document.Records, nil
	}
	cutoff := r.now().Add(-r.retention)
	kept := document.Records[:0]
	removed := false
	for _, record := range document.Records {
		// A zero RequestedAt can occur in a just-created pending record before
		// the caller has attached its request timestamp. It is not evidence of
		// an old audit and must remain durable until explicitly completed.
		if !record.RequestedAt.IsZero() && record.RequestedAt.Before(cutoff) {
			removed = true
			continue
		}
		kept = append(kept, record)
	}
	if !removed {
		return document.Records, nil
	}
	if err := writeAtomic(r.path, auditDocument{SchemaVersion: persistedSchemaVersion, Records: kept}); err != nil {
		return nil, err
	}
	return kept, nil
}

func (r *ResetAuditRepository) Load() ([]domain.ResetAudit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *ResetAuditRepository) FindByKey(key string) (domain.ResetAudit, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked()
	if err != nil {
		return domain.ResetAudit{}, false, err
	}
	for _, item := range items {
		if item.IdempotencyKey == key {
			return item, true, nil
		}
	}
	return domain.ResetAudit{}, false, nil
}

func (r *ResetAuditRepository) AppendPending(audit domain.ResetAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, item := range items {
		if item.IdempotencyKey == audit.IdempotencyKey {
			return &domain.Error{
				Code:       domain.CodeIdempotencyConflict,
				Message:    "idempotency key already exists",
				HTTPStatus: 409,
			}
		}
	}
	items = append(items, audit)
	return writeAtomic(r.path, auditDocument{SchemaVersion: persistedSchemaVersion, Records: items})
}

func (r *ResetAuditRepository) Replace(audit domain.ResetAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked()
	if err != nil {
		return err
	}
	for index, item := range items {
		if item.IdempotencyKey != audit.IdempotencyKey {
			continue
		}
		items[index] = audit
		return writeAtomic(r.path, auditDocument{SchemaVersion: persistedSchemaVersion, Records: items})
	}
	return fmt.Errorf("reset audit record not found: %w", fs.ErrNotExist)
}

func (r *ResetAuditRepository) DeleteBefore(cutoff time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked()
	if err != nil {
		return err
	}
	kept := items[:0]
	removed := false
	for _, item := range items {
		if !item.RequestedAt.IsZero() && item.RequestedAt.Before(cutoff) {
			removed = true
			continue
		}
		kept = append(kept, item)
	}
	if !removed {
		return nil
	}
	return writeAtomic(r.path, auditDocument{SchemaVersion: persistedSchemaVersion, Records: kept})
}

func (r *ResetAuditRepository) RecoverPending(now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked()
	if err != nil {
		return err
	}
	finishedAt := now.UTC()
	changed := false
	for index := range items {
		if items[index].Outcome != domain.ResetPending {
			continue
		}
		items[index].Outcome = domain.ResetUnknown
		items[index].FinishedAt = finishedAt
		changed = true
	}
	if !changed {
		return nil
	}
	return writeAtomic(r.path, auditDocument{SchemaVersion: persistedSchemaVersion, Records: items})
}

func (r *ResetAuditRepository) Clear() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeAtomic(r.path, auditDocument{SchemaVersion: persistedSchemaVersion, Records: []domain.ResetAudit{}})
}
