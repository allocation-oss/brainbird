package handlers

import (
	"context"
	"github.com/segmentio/kafka-go"
	"log"
)

func UploadProcessed(ctx context.Context, message kafka.Message) error {
	log.Printf("upload processed event received: key=%s", string(message.Key))
	return nil
}
