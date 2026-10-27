package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is a single forward-only schema change.
//
// schema.sql stays frozen at version 0 and describes the original schema. Every
// later change belongs in migrations/NNNN_name.sql and is applied exactly once,
// tracked by PRAGMA user_version. Never edit a migration that has shipped.
type migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrate applies every migration newer than the database's user_version.
//
// Each migration runs in its own transaction together with the version bump, so
// a failure leaves the database at the last fully applied version. The caller
// must not run migrations concurrently with another process opening the same
// file.
func Migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	// Pin one connection: PRAGMA user_version is connection-agnostic but the
	// transaction must observe and bump it on the same session.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	var current int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, item := range migrations {
		if item.Version <= current {
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, item.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %04d_%s: %w", item.Version, item.Name, err)
		}
		// PRAGMA does not accept bound parameters.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", item.Version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %04d_%s: %w", item.Version, item.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %04d_%s: %w", item.Version, item.Name, err)
		}
		current = item.Version
	}
	return nil
}

// loadMigrations reads embedded migrations sorted by version.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}
		raw, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, migration{Version: version, Name: name, SQL: string(raw)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	for index := 1; index < len(result); index++ {
		if result[index].Version == result[index-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", result[index].Version)
		}
	}
	return result, nil
}

func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, name, ok := strings.Cut(base, "_")
	if !ok || name == "" {
		return 0, "", fmt.Errorf("migration %q must be named NNNN_name.sql", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version < 1 {
		return 0, "", fmt.Errorf("migration %q has an invalid version prefix", filename)
	}
	return version, name, nil
}

// SchemaVersion reports the applied migration version.
func SchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var current int
	err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current)
	return current, err
}
