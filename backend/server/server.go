package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"brainbird-backend/internal/config"
	"brainbird-backend/internal/middleware"
	"brainbird-backend/internal/platform/database"
	"brainbird-backend/internal/router"
	"github.com/gofiber/fiber/v2"
	fiberCors "github.com/gofiber/fiber/v2/middleware/cors"
	fiberLimiter "github.com/gofiber/fiber/v2/middleware/limiter"
	fiberRecover "github.com/gofiber/fiber/v2/middleware/recover"
)

func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var pool *sql.DB
	if cfg.DatabaseURL != "" {
		pool, err = database.NewPool(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Printf("database unavailable: %v", err)
		}
	}
	if pool != nil {
		defer pool.Close()
	}

	staticDir, err := frontendDir()
	if err != nil {
		return err
	}

	app := fiber.New()
	app.Use(fiberRecover.New(), fiberCors.New(middleware.CORS()), fiberLimiter.New(middleware.RateLimiter()))
	router.RegisterV1(app, pool, cfg.JWTSecret)
	app.Static("/", staticDir)

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(cfg.HTTPAddr)
	}()

	select {
	case <-ctx.Done():
		return app.Shutdown()
	case err := <-listenErr:
		if err != nil {
			return fmt.Errorf("api stopped: %w", err)
		}
		return nil
	}
}

func frontendDir() (string, error) {
	for _, path := range []string{filepath.Join("frontend", "ui"), filepath.Join("..", "frontend", "ui")} {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return path, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect frontend directory %q: %w", path, err)
		}
	}
	return "", fmt.Errorf("frontend assets not found; expected frontend/ui or ../frontend/ui")
}
