package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMigrateReachesLatestVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	st := openTestStore(t, path)

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	latest := migrations[len(migrations)-1].Version
	got, err := SchemaVersion(context.Background(), st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if got != latest {
		t.Fatalf("schema version = %d, want %d", got, latest)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repeat.db")
	first := openTestStore(t, path)
	before := schemaFingerprint(t, first.DB)
	_ = first.Close()

	second := openTestStore(t, path)
	after := schemaFingerprint(t, second.DB)
	if before != after {
		t.Fatalf("schema changed on reopen:\nbefore=%s\nafter=%s", before, after)
	}
}

// TestMigrateUpgradesLegacyDatabase simulates a database created by the previous
// release (schema.sql only, no migrations) and verifies that upgrading preserves
// the provider row and produces the same schema as a fresh install.
func TestMigrateUpgradesLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(raw)); err != nil {
		t.Fatalf("apply v0 schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO users(id,username,display_name,role,created_at)
		VALUES('u1','admin','Admin','admin','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_accounts(provider_id,base_url,api_key_enc,low_balance_threshold,enabled,updated_at)
		VALUES('deepseek','https://api.deepseek.com','v1.ciphertext',10,1,'2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO balance_snapshots(id,user_id,provider_id,currency,total_balance,
			granted_balance,topped_up_balance,is_available,low_balance,source,fetched_at)
		VALUES('b1','u1','deepseek','CNY',12.5,0,12.5,1,0,'deepseek-native','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st := openTestStore(t, path)
	if err := Migrate(ctx, st.DB); err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}

	var kind, displayName, currency, createdAt, apiKey string
	if err := st.DB.QueryRowContext(ctx, `
		SELECT kind,display_name,currency,created_at,COALESCE(api_key_enc,'')
		FROM provider_accounts WHERE provider_id='deepseek'`).
		Scan(&kind, &displayName, &currency, &createdAt, &apiKey); err != nil {
		t.Fatalf("legacy provider row: %v", err)
	}
	if kind != "deepseek" || displayName != "DeepSeek" || currency != "CNY" {
		t.Fatalf("unexpected backfill: kind=%q display=%q currency=%q", kind, displayName, currency)
	}
	if createdAt != "2026-01-02T00:00:00Z" {
		t.Fatalf("created_at not backfilled from updated_at: %q", createdAt)
	}
	if apiKey != "v1.ciphertext" {
		t.Fatalf("encrypted key was modified: %q", apiKey)
	}

	var total float64
	if err := st.DB.QueryRowContext(ctx, `
		SELECT total_balance FROM provider_balances
		WHERE provider_id='deepseek' AND currency='CNY'`).Scan(&total); err != nil {
		t.Fatalf("balance backfill: %v", err)
	}
	if total != 12.5 {
		t.Fatalf("backfilled balance = %v, want 12.5", total)
	}

	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	fresh := openTestStore(t, freshPath)
	if legacy, modern := schemaFingerprint(t, st.DB), schemaFingerprint(t, fresh.DB); legacy != modern {
		t.Fatalf("legacy and fresh schemas differ:\nlegacy=%s\nfresh=%s", legacy, modern)
	}
}

func TestMigrateFailureLeavesVersionUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.db")
	st := openTestStore(t, path)
	ctx := context.Background()
	before, err := SchemaVersion(ctx, st.DB)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE probe (a TEXT)"); err != nil {
		t.Fatal(err)
	}
	// A failing statement must roll back the whole migration.
	if _, err := tx.ExecContext(ctx, "ALTER TABLE probe ADD COLUMN a TEXT"); err == nil {
		t.Fatal("expected the duplicate column to fail")
	}
	_ = tx.Rollback()

	after, err := SchemaVersion(ctx, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("version changed after rollback: %d -> %d", before, after)
	}
	var exists int
	_ = st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='probe'`).Scan(&exists)
	if exists != 0 {
		t.Fatal("failed migration left a table behind")
	}
}

func TestParseMigrationName(t *testing.T) {
	if version, name, err := parseMigrationName("0007_note.sql"); err != nil || version != 7 || name != "note" {
		t.Fatalf("parse 0007_note.sql = (%d,%q,%v)", version, name, err)
	}
	for _, bad := range []string{"note.sql", "0000_note.sql", "abc_note.sql", "0001.sql"} {
		if _, _, err := parseMigrationName(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

// schemaFingerprint captures table definitions so two databases can be compared.
func schemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
		SELECT type,name,COALESCE(sql,'') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var kind, name, sql string
		if err := rows.Scan(&kind, &name, &sql); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, kind+"|"+name+"|"+strings.Join(strings.Fields(sql), " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
