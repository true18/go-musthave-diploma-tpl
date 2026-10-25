// Package migrations runs embedded PostgreSQL migrations.
package migrations

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// FS contains embedded migration files.
//
//go:embed files/*.sql
var FS embed.FS

// Up applies all embedded migrations using an already opened pool.
func Up(pool *pgxpool.Pool) error {
	sourceDriver, err := iofs.New(FS, "files")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	databaseDriver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("create migration database driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", databaseDriver)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
