package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"brainbird-backend/internal/config"
	"brainbird-backend/internal/platform/database"
	"brainbird-backend/pkg/auth"
)

type legacyCredential struct {
	userID   int64
	password string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	if configuration.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := database.NewPool(ctx, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	transaction, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	rows, err := transaction.QueryContext(ctx, "SELECT id, password FROM users WHERE password IS NOT NULL AND CHAR_LENGTH(password) BETWEEN 1 AND 13")
	if err != nil {
		return err
	}
	credentials := make([]legacyCredential, 0)
	for rows.Next() {
		var credential legacyCredential
		if err := rows.Scan(&credential.userID, &credential.password); err != nil {
			rows.Close()
			return err
		}
		if !strings.HasPrefix(credential.password, "$2") {
			credentials = append(credentials, credential)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, credential := range credentials {
		passwordHash, err := auth.HashPassword(credential.password)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, "UPDATE users SET password = ? WHERE id = ? AND password = ?", passwordHash, credential.userID, credential.password); err != nil {
			return err
		}
	}

	if err := transaction.Commit(); err != nil {
		return err
	}
	fmt.Printf("Rehashed %d imported legacy password values.\n", len(credentials))
	return nil
}
