package repositories

//go:generate go tool mockgen -source=category.go -destination=category_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

var (
	// ErrCategoryExists is returned when the user already has the category.
	ErrCategoryExists = errors.New("category already exists")
	// ErrCategoryNotFound is returned when the user has no such category.
	ErrCategoryNotFound = errors.New("category not found")
	// ErrCategoryInUse is returned when expenses still use the category.
	ErrCategoryInUse = errors.New("category is in use")
)

type CategoryRepository interface {
	// List returns the user's categories in the order they were added, with
	// how many of their expenses use each.
	List(ctx context.Context, userId string) ([]models.CategoryUsage, error)
	// Add appends a category. Returns ErrCategoryExists.
	Add(ctx context.Context, userId string, name models.Category, at time.Time) error
	// Remove deletes a category no expense of the user's uses, in one
	// statement so an expense can't take it in between. Returns
	// ErrCategoryInUse or ErrCategoryNotFound.
	Remove(ctx context.Context, userId string, name models.Category) error
	// Seed gives a user without categories the given ones, plus any their
	// expenses already use. It does nothing for a user who has categories.
	Seed(ctx context.Context, userId string, names []models.Category, at time.Time) error
}

type CategoryRepositoryImpl struct {
	client *sqlite.Client
}

// position keeps the order categories were added in, so the defaults stay
// in their usual order and new ones go last.
const categoryTableSchema = `
	CREATE TABLE IF NOT EXISTS categories (
		user_id    TEXT     NOT NULL,
		name       TEXT     NOT NULL,
		position   INTEGER  NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY (user_id, name)
	)
`

// NewCategoryRepository creates the categories table and gives every
// existing user without categories the defaults (plus what their expenses
// use). The expenses and users tables must exist.
func NewCategoryRepository(ctx context.Context, client *sqlite.Client) (CategoryRepository, error) {
	if client == nil {
		panic("category repository: sqlite client must not be nil")
	}
	if _, err := client.ExecContext(ctx, categoryTableSchema); err != nil {
		return nil, fmt.Errorf("create categories table: %w", err)
	}
	repo := &CategoryRepositoryImpl{client: client}

	rows, err := client.QueryContext(ctx, `SELECT id FROM users WHERE id NOT IN (SELECT user_id FROM categories)`)
	if err != nil {
		return nil, fmt.Errorf("find users without categories: %w", err)
	}
	var users []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find users without categories: %w", err)
	}
	now := time.Now()
	for _, id := range users {
		if err := repo.Seed(ctx, id, models.DefaultCategories, now); err != nil {
			return nil, err
		}
	}
	return repo, nil
}

func (r *CategoryRepositoryImpl) List(ctx context.Context, userId string) ([]models.CategoryUsage, error) {
	rows, err := r.client.QueryContext(ctx, `
		SELECT c.name, (SELECT COUNT(*) FROM expenses e WHERE e.user_id = c.user_id AND e.category = c.name)
		FROM categories c
		WHERE c.user_id = ?
		ORDER BY c.position, c.name`, userId)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	out := []models.CategoryUsage{}
	for rows.Next() {
		var (
			c    models.CategoryUsage
			name string
		)
		if err := rows.Scan(&name, &c.Expenses); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		c.Name = models.Category(name)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CategoryRepositoryImpl) Add(ctx context.Context, userId string, name models.Category, at time.Time) error {
	_, err := r.client.ExecContext(ctx, `
		INSERT INTO categories (user_id, name, position, created_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM categories WHERE user_id = ?), ?)`,
		userId, string(name), userId, formatTime(at.UTC()))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrCategoryExists
		}
		return fmt.Errorf("add category: %w", err)
	}
	return nil
}

func (r *CategoryRepositoryImpl) Remove(ctx context.Context, userId string, name models.Category) error {
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			DELETE FROM categories
			WHERE user_id = ? AND name = ?
			  AND NOT EXISTS (SELECT 1 FROM expenses WHERE user_id = ? AND category = ?)`,
			userId, string(name), userId, string(name))
		if err != nil {
			return fmt.Errorf("remove category: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE user_id = ? AND name = ?`, userId, string(name)).Scan(&exists); err != nil {
			return fmt.Errorf("remove category: %w", err)
		}
		if exists == 0 {
			return ErrCategoryNotFound
		}
		return ErrCategoryInUse
	})
}

func (r *CategoryRepositoryImpl) Seed(ctx context.Context, userId string, names []models.Category, at time.Time) error {
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		var has int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE user_id = ?`, userId).Scan(&has); err != nil {
			return fmt.Errorf("seed categories: %w", err)
		}
		if has > 0 {
			return nil
		}
		// Categories the user's expenses already use come after the
		// defaults, so nothing they have is left without one.
		used, err := tx.QueryContext(ctx, `SELECT DISTINCT category FROM expenses WHERE user_id = ? ORDER BY category`, userId)
		if err != nil {
			return fmt.Errorf("seed categories: %w", err)
		}
		all := append([]models.Category{}, names...)
		for used.Next() {
			var c string
			if err := used.Scan(&c); err != nil {
				used.Close()
				return fmt.Errorf("seed categories: %w", err)
			}
			all = append(all, models.Category(c))
		}
		used.Close()
		if err := used.Err(); err != nil {
			return fmt.Errorf("seed categories: %w", err)
		}
		for i, name := range all {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO categories (user_id, name, position, created_at) VALUES (?, ?, ?, ?)`,
				userId, string(name), i+1, formatTime(at.UTC())); err != nil {
				return fmt.Errorf("seed categories: %w", err)
			}
		}
		return nil
	})
}
