package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zajuna-app/core/internal/storage/sqlite"
)

type Snapshotter interface {
	SnapshotDatabase(ctx context.Context, target string) error
}

type Record struct {
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
	SizeBytes int64     `json:"sizeBytes"`
}

type fileDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	FormatVersion string       `json:"formatVersion"`
	CreatedAt     time.Time    `json:"createdAt"`
	Files         []string     `json:"files"`
	Hashes        []fileDigest `json:"hashes,omitempty"`
	SchemaVersion int          `json:"schemaVersion,omitempty"`
	Secrets       string       `json:"secrets"`
}

type appliedRestore struct {
	Items []appliedItem `json:"items"`
}

type appliedItem struct {
	Target string `json:"target"`
	Old    string `json:"old"`
	HadOld bool   `json:"hadOld"`
}

type Manager struct {
	dataDir  string
	snapshot Snapshotter
	mu       sync.Mutex
}

const (
	pendingRestoreDir  = ".restore-pending"
	appliedRestoreFile = ".restore-applied.json"
	maxRestoreBytes    = 250 * 1024 * 1024
)

// RestoreResult describes a validated restore staged for the next process
// start. The database is never replaced while the core is running.
type RestoreResult struct {
	Backup          Record
	SafetyBackup    Record
	RestartRequired bool
}

// List returns published local snapshots without opening their contents.
func (m *Manager) List() ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked()
}

func (m *Manager) listLocked() ([]Record, error) {
	backupDir := filepath.Join(m.dataDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	result := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(strings.ToLower(entry.Name()), ".tmp") || !strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect backup %s: %w", entry.Name(), err)
		}
		result = append(result, Record{Path: filepath.Join(backupDir, entry.Name()), CreatedAt: info.ModTime().UTC(), SizeBytes: info.Size()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func NewManager(dataDir string, snapshot Snapshotter) (*Manager, error) {
	if dataDir == "" {
		return nil, errors.New("backup data directory is required")
	}
	if snapshot == nil {
		return nil, errors.New("backup snapshotter is required")
	}
	return &Manager{dataDir: dataDir, snapshot: snapshot}, nil
}

func (m *Manager) Create(ctx context.Context) (Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	createdAt := time.Now().UTC()
	backupDir := filepath.Join(m.dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return Record{}, fmt.Errorf("create backup directory: %w", err)
	}
	temporaryDir, err := os.MkdirTemp(m.dataDir, ".zajuna-backup-")
	if err != nil {
		return Record{}, fmt.Errorf("create backup workspace: %w", err)
	}
	defer os.RemoveAll(temporaryDir)

	databasePath := filepath.Join(temporaryDir, "database.sqlite")
	if err := m.snapshot.SnapshotDatabase(ctx, databasePath); err != nil {
		return Record{}, err
	}

	files := []string{"database.sqlite"}
	if err := m.copyOptionalData(temporaryDir, &files); err != nil {
		return Record{}, err
	}
	sort.Strings(files)
	if err := sqlite.ValidateRestoredDatabase(databasePath); err != nil {
		return Record{}, fmt.Errorf("el snapshot de SQLite no es restaurable: %w", err)
	}
	hashes, err := hashFiles(temporaryDir, files)
	if err != nil {
		return Record{}, err
	}
	contents, err := json.MarshalIndent(manifest{
		FormatVersion: "1",
		CreatedAt:     createdAt,
		Files:         files,
		Hashes:        hashes,
		SchemaVersion: sqlite.CurrentSchemaVersion(),
		Secrets:       "Las credenciales permanecen en el almacén seguro del sistema y no se incluyen.",
	}, "", "  ")
	if err != nil {
		return Record{}, fmt.Errorf("encode backup manifest: %w", err)
	}
	manifestPath := filepath.Join(temporaryDir, "manifest.json")
	if err := os.WriteFile(manifestPath, append(contents, '\n'), 0o600); err != nil {
		return Record{}, fmt.Errorf("write backup manifest: %w", err)
	}
	files = append(files, "manifest.json")
	sort.Strings(files)

	name := fmt.Sprintf("zajuna-backup-%s.zip", createdAt.Format("20060102-150405.000000000"))
	finalPath := filepath.Join(backupDir, name)
	temporaryZip := finalPath + ".tmp"
	if err := writeZip(temporaryDir, temporaryZip, files); err != nil {
		_ = os.Remove(temporaryZip)
		return Record{}, err
	}
	if err := os.Rename(temporaryZip, finalPath); err != nil {
		_ = os.Remove(temporaryZip)
		return Record{}, fmt.Errorf("publish backup: %w", err)
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		return Record{}, fmt.Errorf("stat backup: %w", err)
	}
	return Record{Path: finalPath, CreatedAt: createdAt, SizeBytes: info.Size()}, nil
}

// Resolve validates a backup name and returns its local record. Callers never
// receive a path from the HTTP API; this method is kept in the storage layer
// so path traversal checks are shared by download, delete and restore.
func (m *Manager) Resolve(name string) (Record, error) {
	path, err := m.resolvePath(name)
	if err != nil {
		return Record{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Record{}, fmt.Errorf("backup not found: %w", err)
	}
	return Record{Path: path, CreatedAt: info.ModTime().UTC(), SizeBytes: info.Size()}, nil
}

func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, err := m.resolvePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	return nil
}

// Cleanup removes only archives older than the requested age and beyond the
// newest keep count. The conservative defaults are applied by the API layer.
func (m *Manager) Cleanup(keep int, olderThan time.Duration) ([]string, error) {
	if keep < 1 {
		keep = 5
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	records, err := m.listLocked()
	if err != nil {
		return nil, err
	}
	cutoff := time.Time{}
	if olderThan > 0 {
		cutoff = time.Now().UTC().Add(-olderThan)
	}
	deleted := make([]string, 0)
	for index, record := range records {
		if index < keep || (!cutoff.IsZero() && record.CreatedAt.After(cutoff)) {
			continue
		}
		if err := os.Remove(record.Path); err != nil {
			return deleted, fmt.Errorf("cleanup backup %s: %w", filepath.Base(record.Path), err)
		}
		deleted = append(deleted, filepath.Base(record.Path))
	}
	return deleted, nil
}

// StageRestore validates and extracts a backup into a private pending folder.
// The caller should create a safety backup immediately before invoking it.
// ApplyPending must run before sqlite.Open on the next process start.
func (m *Manager) StageRestore(ctx context.Context, name string) (Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path, err := m.resolvePath(name)
	if err != nil {
		return Record{}, err
	}
	if _, err := os.Stat(filepath.Join(m.dataDir, pendingRestoreDir)); err == nil {
		return Record{}, errors.New("ya existe una restauración pendiente; reinicia la aplicación para aplicarla")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, fmt.Errorf("inspect pending restore: %w", err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return Record{}, fmt.Errorf("open backup archive: %w", err)
	}
	defer archive.Close()
	manifestValue, err := readManifest(archive.File)
	if err != nil {
		return Record{}, err
	}
	files, err := validateManifest(manifestValue, archive.File)
	if err != nil {
		return Record{}, err
	}
	temporaryDir, err := os.MkdirTemp(m.dataDir, ".zajuna-restore-")
	if err != nil {
		return Record{}, fmt.Errorf("create restore workspace: %w", err)
	}
	defer os.RemoveAll(temporaryDir)
	var total int64
	expectedHashes := hashMap(manifestValue.Hashes)
	for _, file := range archive.File {
		if file.Name == "manifest.json" {
			continue
		}
		if !files[file.Name] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Record{}, err
		}
		if file.UncompressedSize64 > uint64(maxRestoreBytes) || total+int64(file.UncompressedSize64) > maxRestoreBytes {
			return Record{}, errors.New("el backup supera el límite seguro de restauración")
		}
		cleanName, err := safeArchivePath(file.Name)
		if err != nil {
			return Record{}, err
		}
		target := filepath.Join(temporaryDir, filepath.FromSlash(cleanName))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return Record{}, fmt.Errorf("create restore file directory: %w", err)
		}
		input, err := file.Open()
		if err != nil {
			return Record{}, fmt.Errorf("open backup member %s: %w", file.Name, err)
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			input.Close()
			return Record{}, fmt.Errorf("create restore member %s: %w", file.Name, err)
		}
		hasher := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(output, hasher), input, int64(file.UncompressedSize64))
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil && !(copyErr == io.EOF && written == int64(file.UncompressedSize64)) {
			return Record{}, fmt.Errorf("extract backup member %s: %w", file.Name, copyErr)
		}
		if closeInputErr != nil || closeOutputErr != nil {
			return Record{}, fmt.Errorf("close restored member %s", file.Name)
		}
		if expected := expectedHashes[cleanName]; expected != "" {
			if got := hex.EncodeToString(hasher.Sum(nil)); got != expected {
				return Record{}, fmt.Errorf("el hash de %s no coincide con el manifiesto", file.Name)
			}
		}
		total += written
	}
	if len(expectedHashes) > 0 {
		for name := range files {
			if expectedHashes[name] == "" {
				return Record{}, fmt.Errorf("el manifiesto no declara hash para %s", name)
			}
		}
	}
	if err := verifyStagedDatabase(temporaryDir, manifestValue); err != nil {
		return Record{}, err
	}
	manifestBytes, _ := json.MarshalIndent(manifestValue, "", "  ")
	if err := os.WriteFile(filepath.Join(temporaryDir, "manifest.json"), append(manifestBytes, '\n'), 0o600); err != nil {
		return Record{}, fmt.Errorf("write pending restore manifest: %w", err)
	}
	pendingPath := filepath.Join(m.dataDir, pendingRestoreDir)
	if err := os.Rename(temporaryDir, pendingPath); err != nil {
		return Record{}, fmt.Errorf("publish pending restore: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Record{}, err
	}
	return Record{Path: path, CreatedAt: info.ModTime().UTC(), SizeBytes: info.Size()}, nil
}

// ApplyPending applies a staged restore before the database is opened. It
// returns false when no restore is pending.
func ApplyPending(dataDir string) (bool, error) {
	pendingPath := filepath.Join(dataDir, pendingRestoreDir)
	if _, err := os.Stat(pendingPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect pending restore: %w", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(pendingPath, "manifest.json"))
	if err != nil {
		return false, fmt.Errorf("read pending restore manifest: %w", err)
	}
	var manifestValue manifest
	if err := json.Unmarshal(manifestBytes, &manifestValue); err != nil {
		return false, fmt.Errorf("decode pending restore manifest: %w", err)
	}
	if manifestValue.FormatVersion != "1" || !containsFile(manifestValue.Files, "database.sqlite") {
		return false, errors.New("la restauración pendiente no tiene un manifiesto compatible")
	}
	if err := verifyStagedDatabase(pendingPath, manifestValue); err != nil {
		_ = os.RemoveAll(pendingPath)
		return false, err
	}
	paths := []string{"database.sqlite"}
	seen := map[string]bool{"database.sqlite": true}
	for _, file := range manifestValue.Files {
		if file == "manifest.json" || seen[file] {
			continue
		}
		cleanName, err := safeArchivePath(file)
		if err != nil {
			return false, err
		}
		if !allowedRestorePath(cleanName) {
			return false, fmt.Errorf("el backup contiene una ruta no permitida: %s", file)
		}
		seen[file] = true
		paths = append(paths, cleanName)
	}
	for _, file := range paths {
		if _, err := os.Stat(filepath.Join(pendingPath, filepath.FromSlash(file))); err != nil {
			return false, fmt.Errorf("falta el archivo de restauración %s: %w", file, err)
		}
	}
	type replacement struct {
		target, old, source string
		hadOld              bool
	}
	replacements := make([]replacement, 0, len(paths))
	rollback := func() {
		for index := len(replacements) - 1; index >= 0; index-- {
			item := replacements[index]
			_ = os.RemoveAll(item.target)
			if item.hadOld {
				_ = os.Rename(item.old, item.target)
			}
		}
	}
	for _, file := range paths {
		if file == "manifest.json" {
			continue
		}
		source := filepath.Join(pendingPath, filepath.FromSlash(file))
		target := filepath.Join(dataDir, filepath.FromSlash(file))
		if file == "database.sqlite" {
			target = filepath.Join(dataDir, "zajuna.db")
		}
		old := target + ".restore-old"
		_ = os.RemoveAll(old)
		item := replacement{target: target, old: old, source: source}
		if _, statErr := os.Stat(target); statErr == nil {
			if err := os.Rename(target, old); err != nil {
				rollback()
				return false, fmt.Errorf("prepare restore target %s: %w", file, err)
			}
			item.hadOld = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			rollback()
			return false, fmt.Errorf("inspect restore target %s: %w", file, statErr)
		}
		replacements = append(replacements, item)
		if err := os.Rename(source, target); err != nil {
			rollback()
			return false, fmt.Errorf("apply restore target %s: %w", file, err)
		}
		_ = os.Chmod(target, 0o600)
	}
	marker := appliedRestore{Items: make([]appliedItem, 0, len(replacements))}
	for _, item := range replacements {
		marker.Items = append(marker.Items, appliedItem{Target: item.target, Old: item.old, HadOld: item.hadOld})
	}
	markerBytes, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		rollback()
		return false, fmt.Errorf("encode restore marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, appliedRestoreFile), append(markerBytes, '\n'), 0o600); err != nil {
		rollback()
		return false, fmt.Errorf("write restore marker: %w", err)
	}
	_ = os.RemoveAll(filepath.Join(dataDir, "zajuna.db-wal"))
	_ = os.RemoveAll(filepath.Join(dataDir, "zajuna.db-shm"))
	if err := os.RemoveAll(pendingPath); err != nil {
		return false, fmt.Errorf("remove pending restore: %w", err)
	}
	return true, nil
}

// RollbackApplied puts the previous live files back when sqlite.Open or
// migration fails after a swap. The active database is restored from
// `.restore-old` copies; nothing from the rejected backup remains in place.
func RollbackApplied(dataDir string) error {
	marker, err := readAppliedRestore(dataDir)
	if err != nil {
		return err
	}
	if marker == nil {
		return nil
	}
	for index := len(marker.Items) - 1; index >= 0; index-- {
		item := marker.Items[index]
		_ = os.RemoveAll(item.Target)
		if item.HadOld {
			if err := os.Rename(item.Old, item.Target); err != nil {
				return fmt.Errorf("rollback restore target: %w", err)
			}
		}
	}
	_ = os.Remove(filepath.Join(dataDir, appliedRestoreFile))
	return nil
}

// CommitApplied drops the previous live copies after the restored database
// opened and migrated successfully.
func CommitApplied(dataDir string) error {
	marker, err := readAppliedRestore(dataDir)
	if err != nil {
		return err
	}
	if marker == nil {
		return nil
	}
	for _, item := range marker.Items {
		_ = os.RemoveAll(item.Old)
	}
	if err := os.Remove(filepath.Join(dataDir, appliedRestoreFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("commit restore: %w", err)
	}
	return nil
}

func readAppliedRestore(dataDir string) (*appliedRestore, error) {
	contents, err := os.ReadFile(filepath.Join(dataDir, appliedRestoreFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read restore marker: %w", err)
	}
	var marker appliedRestore
	if err := json.Unmarshal(contents, &marker); err != nil {
		return nil, fmt.Errorf("decode restore marker: %w", err)
	}
	return &marker, nil
}

func (m *Manager) resolvePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) || !strings.HasSuffix(strings.ToLower(name), ".zip") {
		return "", errors.New("nombre de backup inválido")
	}
	backupDir := filepath.Join(m.dataDir, "backups")
	path := filepath.Join(backupDir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("backup inválido")
	}
	return path, nil
}

func readManifest(files []*zip.File) (manifest, error) {
	for _, file := range files {
		if file.Name != "manifest.json" {
			continue
		}
		input, err := file.Open()
		if err != nil {
			return manifest{}, fmt.Errorf("open backup manifest: %w", err)
		}
		contents, readErr := io.ReadAll(io.LimitReader(input, 64*1024))
		_ = input.Close()
		if readErr != nil {
			return manifest{}, fmt.Errorf("read backup manifest: %w", readErr)
		}
		var result manifest
		if err := json.Unmarshal(contents, &result); err != nil {
			return manifest{}, fmt.Errorf("decode backup manifest: %w", err)
		}
		return result, nil
	}
	return manifest{}, errors.New("el backup no contiene manifest.json")
}

func validateManifest(value manifest, files []*zip.File) (map[string]bool, error) {
	if value.FormatVersion != "1" || !containsFile(value.Files, "database.sqlite") {
		return nil, errors.New("el backup tiene un formato incompatible")
	}
	if value.SchemaVersion > sqlite.CurrentSchemaVersion() {
		return nil, fmt.Errorf("el schema del backup (%d) es incompatible con esta versión (%d)", value.SchemaVersion, sqlite.CurrentSchemaVersion())
	}
	allowed := map[string]bool{"manifest.json": true}
	for _, name := range value.Files {
		cleanName, err := safeArchivePath(name)
		if err != nil {
			return nil, err
		}
		if cleanName != "database.sqlite" && !allowedRestorePath(cleanName) && cleanName != "manifest.json" {
			return nil, fmt.Errorf("el backup contiene una ruta no permitida: %s", name)
		}
		allowed[cleanName] = true
	}
	archiveNames := map[string]bool{}
	for _, file := range files {
		cleanName, err := safeArchivePath(file.Name)
		if err != nil {
			return nil, err
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("el backup contiene un enlace simbólico: %s", file.Name)
		}
		archiveNames[cleanName] = true
	}
	for name := range allowed {
		if !archiveNames[name] {
			return nil, fmt.Errorf("falta el miembro del backup: %s", name)
		}
	}
	for name := range archiveNames {
		if !allowed[name] {
			return nil, fmt.Errorf("miembro no declarado en el manifiesto: %s", name)
		}
	}
	delete(allowed, "manifest.json")
	return allowed, nil
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return "", errors.New("ruta de backup inválida")
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("ruta de backup fuera del destino: %s", name)
	}
	return clean, nil
}

func allowedRestorePath(name string) bool {
	return name == "config.json" || strings.HasPrefix(name, "evidences/") || strings.HasPrefix(name, "reports/") || strings.HasPrefix(name, "exports/")
}

func containsFile(files []string, wanted string) bool {
	for _, file := range files {
		if file == wanted {
			return true
		}
	}
	return false
}

func verifyStagedDatabase(dir string, value manifest) error {
	if value.SchemaVersion > sqlite.CurrentSchemaVersion() {
		return fmt.Errorf("el schema del backup (%d) es incompatible con esta versión (%d)", value.SchemaVersion, sqlite.CurrentSchemaVersion())
	}
	databasePath := filepath.Join(dir, "database.sqlite")
	info, err := os.Lstat(databasePath)
	if err != nil {
		return fmt.Errorf("falta database.sqlite en el backup: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("database.sqlite del backup no es un archivo regular")
	}
	if err := os.Chmod(databasePath, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("ajustar permisos de la base restaurada: %w", err)
	}
	if err := sqlite.ValidateRestoredDatabase(databasePath); err != nil {
		return err
	}
	return nil
}

func hashFiles(dir string, files []string) ([]fileDigest, error) {
	result := make([]fileDigest, 0, len(files))
	for _, name := range files {
		if name == "manifest.json" {
			continue
		}
		sum, err := fileSHA256(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		result = append(result, fileDigest{Name: name, SHA256: sum})
	}
	return result, nil
}

func hashMap(hashes []fileDigest) map[string]string {
	result := make(map[string]string, len(hashes))
	for _, item := range hashes {
		if item.Name != "" {
			result[item.Name] = item.SHA256
		}
	}
	return result
}

func fileSHA256(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash backup file %s: %w", path, err)
	}
	defer input.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, input); err != nil {
		return "", fmt.Errorf("hash backup file %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (m *Manager) copyOptionalData(workspace string, files *[]string) error {
	// This whitelist intentionally excludes config secrets, browser profiles,
	// temporary files and previous backups. Credentials live in the OS keyring.
	for _, name := range []string{"config.json", "evidences", "reports", "exports"} {
		source := filepath.Join(m.dataDir, name)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect backup data %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup data cannot be a symlink: %s", name)
		}
		target := filepath.Join(workspace, name)
		if info.IsDir() {
			if err := copyDir(source, target, files, name); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(source, target); err != nil {
			return err
		}
		*files = append(*files, name)
	}
	return nil
}

func copyDir(source, target string, files *[]string, prefix string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(target, 0o700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup data cannot contain symlinks: %s", path)
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := copyFile(path, destination); err != nil {
			return err
		}
		*files = append(*files, filepath.ToSlash(filepath.Join(prefix, relative)))
		return nil
	})
}

func copyFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create backup file directory: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open backup file %s: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create backup file %s: %w", target, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy backup file %s: %w", source, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close backup file %s: %w", target, err)
	}
	return nil
}

func writeZip(sourceDir, target string, files []string) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	archive := zip.NewWriter(file)
	for _, name := range files {
		cleanName := filepath.ToSlash(filepath.Clean(name))
		if cleanName == "." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") {
			_ = archive.Close()
			_ = file.Close()
			return fmt.Errorf("invalid backup archive path: %s", name)
		}
		input, err := os.Open(filepath.Join(sourceDir, name))
		if err != nil {
			_ = archive.Close()
			_ = file.Close()
			return fmt.Errorf("open archive member %s: %w", name, err)
		}
		entry, err := archive.Create(cleanName)
		if err == nil {
			_, err = io.Copy(entry, input)
		}
		_ = input.Close()
		if err != nil {
			_ = archive.Close()
			_ = file.Close()
			return fmt.Errorf("write archive member %s: %w", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("close backup archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup file: %w", err)
	}
	return nil
}
