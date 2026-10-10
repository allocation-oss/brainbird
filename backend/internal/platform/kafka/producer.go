package kafka

import (
	"context"
	"github.com/segmentio/kafka-go"
)

type Producer struct{ writer *kafka.Writer }

func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{writer: &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topic, Balancer: &kafka.LeastBytes{}}}
}

func (p *Producer) Publish(ctx context.Context, key, value []byte) error {
	return p.writer.WriteMessages(ctx, kafka.Message{Key: key, Value: value})
}
func (p *Producer) Close() error { return p.writer.Close() }
