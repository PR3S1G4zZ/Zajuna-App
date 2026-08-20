package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRestoredDatabaseAcceptsMigratedSnapshot(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRestoredDatabase(filepath.Join(dir, "zajuna.db")); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRestoredDatabaseRejectsEmptyAndNonSqliteFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRestoredDatabase(empty); err == nil {
		t.Fatal("expected empty database to be rejected")
	}
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRestoredDatabase(garbage); err == nil {
		t.Fatal("expected non-sqlite file to be rejected")
	}
}
