package main

import (
	"brainbird-backend/internal/config"
	"brainbird-backend/internal/platform/kafka"
	"brainbird-backend/worker/handlers"
	"context"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	consumer := kafka.NewConsumer(cfg.KafkaBrokers, "upload.processed", cfg.KafkaGroupID)
	defer consumer.Close()
	if err := consumer.Run(ctx, handlers.UploadProcessed); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
