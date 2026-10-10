package database

import (
	"context"
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
)

func NewPool(ctx context.Context, databaseURL string) (*sql.DB, error) {
	pool, err := sql.Open("mysql", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
