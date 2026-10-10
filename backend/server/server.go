package server

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"

	"brainbird-backend/internal/config"
	"brainbird-backend/internal/middleware"
	"brainbird-backend/internal/platform/database"
	"brainbird-backend/internal/router"
	brainbirdui "brainbird/frontend/ui"
	"github.com/gofiber/fiber/v2"
	fiberCors "github.com/gofiber/fiber/v2/middleware/cors"
	fiberFilesystem "github.com/gofiber/fiber/v2/middleware/filesystem"
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

	staticFiles, err := fs.Sub(brainbirdui.Files, ".")
	if err != nil {
		return fmt.Errorf("open embedded frontend assets: %w", err)
	}

	app := fiber.New()
	app.Use(fiberRecover.New(), fiberCors.New(middleware.CORS()), fiberLimiter.New(middleware.RateLimiter()))
	router.RegisterV1(app, pool, cfg.JWTSecret)
	app.Use("/", fiberFilesystem.New(fiberFilesystem.Config{
		Root:  embeddedHTTPFS{FileSystem: http.FS(staticFiles)},
		Index: "index.html",
	}))

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

type embeddedHTTPFS struct {
	http.FileSystem
}

func (f embeddedHTTPFS) Open(name string) (http.File, error) {
	name = strings.TrimLeft(name, "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	return f.FileSystem.Open(name)
}
