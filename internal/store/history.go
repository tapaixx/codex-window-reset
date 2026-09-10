package store

import (
	"path/filepath"
	"sync"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type historyDocument struct {
	SchemaVersion int                      `json:"schema_version"`
	Records       []domain.OperationRecord `json:"records"`
}

type HistoryRepository struct {
	mu         sync.Mutex
	path       string
	maxRecords int
}

func NewHistoryRepository(dir string, maxRecords int) *HistoryRepository {
	if maxRecords < 0 {
		maxRecords = 0
	}
	return &HistoryRepository{
		path:       filepath.Join(dir, "history.json"),
		maxRecords: maxRecords,
	}
}

func (r *HistoryRepository) Load() ([]domain.OperationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *HistoryRepository) loadLocked() ([]domain.OperationRecord, error) {
	var document historyDocument
	err := readJSON(r.path, &document)
	if isMissing(err) {
		return []domain.OperationRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	if document.SchemaVersion != persistedSchemaVersion {
		return nil, unsupportedSchema(r.path, document.SchemaVersion)
	}
	if document.Records == nil {
		document.Records = []domain.OperationRecord{}
	}
	return document.Records, nil
}

func (r *HistoryRepository) Append(record domain.OperationRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked()
	if err != nil {
		return err
	}
	items = append(items, record)
	if len(items) > r.maxRecords {
		items = items[len(items)-r.maxRecords:]
	}
	if items == nil {
		items = []domain.OperationRecord{}
	}
	return writeAtomic(r.path, historyDocument{SchemaVersion: persistedSchemaVersion, Records: items})
}

func (r *HistoryRepository) Clear() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeAtomic(r.path, historyDocument{SchemaVersion: persistedSchemaVersion, Records: []domain.OperationRecord{}})
}
