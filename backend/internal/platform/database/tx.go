package database

import (
	"context"
	"database/sql"
)

func WithTx(ctx context.Context, pool *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
