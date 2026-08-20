package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zajuna-app/core/internal/storage/sqlite"
)

func TestCreateBackupIncludesSnapshotAndOmitsCredentials(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{"zajunaUsername":"user","credentialsStored":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "evidences"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "evidences", "capture.png"), []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	seen := map[string]bool{}
	for _, entry := range archive.File {
		seen[entry.Name] = true
		if entry.Name == "config.json" {
			contents, _ := entry.Open()
			var config map[string]any
			if err := json.NewDecoder(contents).Decode(&config); err != nil {
				t.Fatal(err)
			}
			_ = contents.Close()
			if config["zajunaPassword"] != nil {
				t.Fatal("backup must not contain a password")
			}
		}
	}
	for _, name := range []string{"database.sqlite", "config.json", "evidences/capture.png", "manifest.json"} {
		if !seen[name] {
			t.Fatalf("backup is missing %s", name)
		}
	}
	if seen["zajuna.db"] || seen["backups"] {
		t.Fatal("backup contains excluded data")
	}
}

func TestStageAndApplyRestoreBeforeOpeningDatabase(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppSetting(context.Background(), "restore-marker", "before"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{"version":"before"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppSetting(context.Background(), "restore-marker", "after"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{"version":"after"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StageRestore(context.Background(), filepath.Base(record.Path)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPending(dataDir)
	if err != nil || !applied {
		t.Fatalf("restore was not applied: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil || string(contents) != `{"version":"before"}` {
		t.Fatalf("config was not restored: %q (%v)", contents, err)
	}
	restored, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	marker, err := restored.GetAppSetting(context.Background(), "restore-marker")
	if err != nil || marker != "before" {
		t.Fatalf("database was not restored: %q (%v)", marker, err)
	}
	if err := CommitApplied(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "zajuna.db.restore-old")); !os.IsNotExist(err) {
		t.Fatal("safety copy should be removed after a successful restore")
	}
}

func TestStageRestoreRejectsCorruptDatabaseWithoutTouchingLiveFiles(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAppSetting(context.Background(), "restore-marker", "live"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(dataDir, "backups", "zajuna-backup-corrupt.zip")
	writeTestArchive(t, corrupt, map[string][]byte{
		"database.sqlite": []byte("this is not sqlite"),
		"manifest.json":   []byte(`{"formatVersion":"1","files":["database.sqlite"],"secrets":"none"}` + "\n"),
	})
	if _, err := manager.StageRestore(context.Background(), filepath.Base(corrupt)); err == nil {
		t.Fatal("expected corrupt backup to be rejected")
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".restore-pending")); !os.IsNotExist(err) {
		t.Fatal("corrupt restore must not leave a pending folder")
	}
	marker, err := store.GetAppSetting(context.Background(), "restore-marker")
	if err != nil || marker != "live" {
		t.Fatalf("live database was modified: %q (%v)", marker, err)
	}
}

func TestStageRestoreRejectsHashMismatch(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	tampered := filepath.Join(dataDir, "backups", "zajuna-backup-tampered.zip")
	writeTestArchive(t, tampered, map[string][]byte{
		"database.sqlite": []byte("tampered-db"),
		"manifest.json":   []byte(`{"formatVersion":"1","files":["database.sqlite"],"hashes":[{"name":"database.sqlite","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"secrets":"none"}` + "\n"),
	})
	if _, err := manager.StageRestore(context.Background(), filepath.Base(tampered)); err == nil {
		t.Fatal("expected hash mismatch to be rejected")
	}
}

func TestApplyPendingRollsBackWhenRestoredDatabaseCannotOpen(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppSetting(context.Background(), "restore-marker", "before"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppSetting(context.Background(), "restore-marker", "after"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StageRestore(context.Background(), filepath.Base(record.Path)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPending(dataDir)
	if err != nil || !applied {
		t.Fatalf("restore was not applied: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "zajuna.db"), []byte("broken-after-swap"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(dataDir); err == nil {
		t.Fatal("expected swapped garbage database to fail open")
	}
	if err := RollbackApplied(dataDir); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	marker, err := restored.GetAppSetting(context.Background(), "restore-marker")
	if err != nil || marker != "after" {
		t.Fatalf("rollback did not restore the previous database: %q (%v)", marker, err)
	}
}

func writeTestArchive(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, contents := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteBackupRejectsTraversal(t *testing.T) {
	manager, err := NewManager(t.TempDir(), &fakeSnapshotter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(`..\outside.zip`); err == nil {
		t.Fatal("expected traversal name to be rejected")
	}
}

func TestCleanupKeepsNewestAndOnlyRemovesOldArchives(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(first.Path, time.Now().Add(-72*time.Hour), time.Now().Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := manager.Cleanup(1, 24*time.Hour)
	if err != nil || len(deleted) != 1 || deleted[0] != filepath.Base(first.Path) {
		t.Fatalf("unexpected cleanup result: %#v (%v)", deleted, err)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Fatalf("newest backup should remain: %v", err)
	}
}

type fakeSnapshotter struct{}

func (fakeSnapshotter) SnapshotDatabase(_ context.Context, target string) error {
	return os.WriteFile(target, []byte("snapshot"), 0o600)
}
