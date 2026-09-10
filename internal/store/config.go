package store

import (
	"path/filepath"
	"sync"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type ConfigRepository struct {
	mu   sync.Mutex
	path string
}

func NewConfigRepository(dir string) *ConfigRepository {
	return &ConfigRepository{path: filepath.Join(dir, "config.json")}
}

func (r *ConfigRepository) Load() (domain.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *ConfigRepository) loadLocked() (domain.Config, error) {
	var config domain.Config
	err := readJSON(r.path, &config)
	if isMissing(err) {
		config = domain.DefaultConfig()
		if err := writeAtomic(r.path, config); err != nil {
			return domain.Config{}, err
		}
		return config, nil
	}
	if err != nil {
		return domain.Config{}, err
	}
	if config.SchemaVersion != persistedSchemaVersion {
		return domain.Config{}, unsupportedSchema(r.path, config.SchemaVersion)
	}
	return config, nil
}

func (r *ConfigRepository) Save(config domain.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if config.SchemaVersion == 0 {
		config.SchemaVersion = persistedSchemaVersion
	}
	if config.SchemaVersion != persistedSchemaVersion {
		return unsupportedSchema(r.path, config.SchemaVersion)
	}
	return writeAtomic(r.path, config)
}
