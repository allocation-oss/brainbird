package main

import (
	"context"
	"database/sql"
	"log"
	"os/signal"
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

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
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
	app := fiber.New()
	app.Use(fiberRecover.New(), fiberCors.New(middleware.CORS()), fiberLimiter.New(middleware.RateLimiter()))
	router.RegisterV1(app, pool, cfg.JWTSecret)
	app.Static("/", "../frontend/ui")
	go func() {
		if err := app.Listen(cfg.HTTPAddr); err != nil {
			log.Printf("api stopped: %v", err)
		}
	}()
	<-ctx.Done()
	_ = app.Shutdown()
}
