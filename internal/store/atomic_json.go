package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

const persistedSchemaVersion = 1

// renameFile is a narrow seam around os.Rename so atomic-write failure paths
// can be tested without changing the filesystem's rename semantics.
var renameFile = os.Rename

func writeAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".window-reset-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = renameFile(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return corruptStore(path)
	}
	return nil
}

func corruptStore(path string) error {
	return &domain.Error{
		Code:       domain.CodeStoreCorrupt,
		Message:    fmt.Sprintf("store document %q is corrupt", filepath.Base(path)),
		HTTPStatus: 500,
	}
}

func unsupportedSchema(path string, got int) error {
	return &domain.Error{
		Code:       domain.CodeStoreCorrupt,
		Message:    fmt.Sprintf("store document %q uses unsupported schema version %d", filepath.Base(path), got),
		HTTPStatus: 500,
	}
}

func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }
