package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// CurrentSchemaVersion is the newest migration the running core can open.
func CurrentSchemaVersion() int {
	return currentSchemaVersion
}

// ValidateRestoredDatabase inspects a snapshot before it replaces the live
// database. The live file is never opened here.
func ValidateRestoredDatabase(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect restored database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("la base restaurada no es un archivo regular")
	}
	if info.Size() == 0 {
		return errors.New("la base restaurada está vacía")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("abrir base restaurada: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		return fmt.Errorf("la base restaurada no se pudo abrir en modo lectura: %w", err)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("integrity_check de la base restaurada: %w", err)
	}
	defer rows.Close()
	ok := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("leer integrity_check: %w", err)
		}
		if result != "ok" {
			return fmt.Errorf("integrity_check rechazó la base restaurada: %s", result)
		}
		ok = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("leer integrity_check: %w", err)
	}
	if !ok {
		return errors.New("integrity_check no devolvió un resultado")
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return errors.New("el backup no incluye schema_migrations")
	}
	if version < 1 || version > currentSchemaVersion {
		return fmt.Errorf("el schema del backup (%d) es incompatible con esta versión (%d)", version, currentSchemaVersion)
	}
	for _, table := range []string{"schema_migrations", "jobs", "fichas", "evidences"} {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			return fmt.Errorf("el backup no incluye la tabla %s", table)
		}
	}
	return nil
}
