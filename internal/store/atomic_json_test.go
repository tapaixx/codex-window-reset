package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicRenameFailurePreservesOriginalAndCleansTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "document.json")
	original := []byte("{\"schema_version\":1,\"value\":\"old\"}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	previousRename := renameFile
	t.Cleanup(func() { renameFile = previousRename })
	renameFile = func(string, string) error { return errors.New("rename blocked") }

	err := writeAtomic(path, map[string]any{"schema_version": 1, "value": "new"})
	if err == nil {
		t.Fatal("writeAtomic unexpectedly succeeded")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("original document changed after failed rename: %q", got)
	}
	temporary, err := filepath.Glob(filepath.Join(dir, ".window-reset-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files left after failed rename: %v", temporary)
	}
}

func TestWriteAtomicCreatesParentWithPrivateIndentedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "document.json")
	if err := writeAtomic(path, struct {
		SchemaVersion int    `json:"schema_version"`
		Value         string `json:"value"`
	}{SchemaVersion: 1, Value: "ok"}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{\n  \"schema_version\": 1,\n  \"value\": \"ok\"\n}\n"
	if string(got) != expected {
		t.Fatalf("unexpected JSON document: %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("document mode = %o, want 600", mode)
	}
}
