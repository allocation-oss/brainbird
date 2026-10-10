package kafka

import (
	"context"
	"github.com/segmentio/kafka-go"
)

type Consumer struct{ reader *kafka.Reader }

func NewConsumer(brokers []string, topic, groupID string) *Consumer {
	return &Consumer{reader: kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: groupID, MinBytes: 1, MaxBytes: 10e6})}
}
func (c *Consumer) Run(ctx context.Context, handler func(context.Context, kafka.Message) error) error {
	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			return err
		}
		if err := handler(ctx, msg); err != nil {
			return err
		}
	}
}
func (c *Consumer) Close() error { return c.reader.Close() }
