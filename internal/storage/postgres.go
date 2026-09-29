package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	*sql.DB
}

// Connect opens a PostgreSQL connection and sets up sensible connection pooling.
func Connect(databaseURL string) (*DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{DB: db}, nil
}

// RunMigrations executes embedded SQL migrations.
func (db *DB) RunMigrations(ctx context.Context) error {
	migrationFiles := []string{
		"migrations/000001_init_schema.up.sql",
		"migrations/000002_piket_lele.up.sql",
	}

	for _, file := range migrationFiles {
		migrationSQL, err := migrationsFS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		_, err = db.ExecContext(ctx, string(migrationSQL))
		if err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", file, err)
		}
	}

	log.Println("[Database] All migrations executed successfully")
	return nil
}
