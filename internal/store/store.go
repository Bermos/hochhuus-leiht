// Package store keeps the lending list in Postgres.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no item has the requested id.
var ErrNotFound = errors.New("item not found")

// Item is one thing a resident offers to lend.
type Item struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Name        string    `json:"name"`
	Floor       string    `json:"floor"`
	Contact     string    `json:"contact"`
	Conditions  string    `json:"conditions"`
	Status      string    `json:"status"`
	OwnerID     string    `json:"-"`
	ImageKey    string    `json:"-"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Store wraps a connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to the database named by url.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connecting to the database: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Ping reports whether the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// migrations are applied in order, each exactly once. They are forward-only:
// append to the list, never edit an entry that has shipped.
var migrations = []string{
	`CREATE TABLE items (
		id          text PRIMARY KEY,
		title       text NOT NULL,
		category    text NOT NULL,
		description text NOT NULL DEFAULT '',
		name        text NOT NULL,
		floor       text NOT NULL DEFAULT '',
		contact     text NOT NULL,
		conditions  text NOT NULL DEFAULT '',
		status      text NOT NULL,
		owner_id    text NOT NULL DEFAULT '',
		image_key   text NOT NULL DEFAULT '',
		created_at  timestamptz NOT NULL DEFAULT now(),
		updated_at  timestamptz NOT NULL DEFAULT now()
	)`,
}

// Migrate brings the schema up to date. It is safe to run any number of times.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Serialise concurrent runs.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4711)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var current int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		if _, err := tx.Exec(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, i+1); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const columns = `id, title, category, description, name, floor, contact, conditions, status, owner_id, image_key, created_at, updated_at`

func scan(row pgx.Row) (Item, error) {
	var it Item
	err := row.Scan(&it.ID, &it.Title, &it.Category, &it.Description, &it.Name, &it.Floor,
		&it.Contact, &it.Conditions, &it.Status, &it.OwnerID, &it.ImageKey, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// List returns every item, newest first.
func (s *Store) List(ctx context.Context) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM items ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// Get returns one item.
func (s *Store) Get(ctx context.Context, id string) (Item, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM items WHERE id = $1`, id))
}

// Create inserts a new item.
func (s *Store) Create(ctx context.Context, it Item) (Item, error) {
	return scan(s.pool.QueryRow(ctx, `
		INSERT INTO items (id, title, category, description, name, floor, contact, conditions, status, owner_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+columns,
		it.ID, it.Title, it.Category, it.Description, it.Name, it.Floor, it.Contact, it.Conditions, it.Status, it.OwnerID))
}

// Update replaces the editable fields of an item.
func (s *Store) Update(ctx context.Context, it Item) (Item, error) {
	return scan(s.pool.QueryRow(ctx, `
		UPDATE items SET title = $2, category = $3, description = $4, name = $5, floor = $6,
			contact = $7, conditions = $8, status = $9, updated_at = now()
		WHERE id = $1
		RETURNING `+columns,
		it.ID, it.Title, it.Category, it.Description, it.Name, it.Floor, it.Contact, it.Conditions, it.Status))
}

// SetImage records the object key of an item's picture; "" removes it.
func (s *Store) SetImage(ctx context.Context, id, key string) (Item, error) {
	return scan(s.pool.QueryRow(ctx, `
		UPDATE items SET image_key = $2, updated_at = now() WHERE id = $1 RETURNING `+columns, id, key))
}

// Delete removes an item and returns what it was.
func (s *Store) Delete(ctx context.Context, id string) (Item, error) {
	return scan(s.pool.QueryRow(ctx, `DELETE FROM items WHERE id = $1 RETURNING `+columns, id))
}

// Import inserts items that do not exist yet and leaves existing ids alone.
// It answers how many were added.
func (s *Store) Import(ctx context.Context, items []Item, ownerID string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	added := 0
	for _, it := range items {
		tag, err := tx.Exec(ctx, `
			INSERT INTO items (id, title, category, description, name, floor, contact, conditions, status, owner_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO NOTHING`,
			it.ID, it.Title, it.Category, it.Description, it.Name, it.Floor, it.Contact, it.Conditions, it.Status, ownerID)
		if err != nil {
			return 0, err
		}
		added += int(tag.RowsAffected())
	}
	return added, tx.Commit(ctx)
}
