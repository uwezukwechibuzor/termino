package kafka

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type Producer struct {
	writer *kafka.Writer
	logger *zap.Logger
}

func NewProducer(brokers []string, topic string, logger *zap.Logger) *Producer {
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{}, // partition by key (symbol)
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		RequiredAcks: kafka.RequireOne,
		Async:        false,
		MaxAttempts:  3,
	}

	return &Producer{writer: w, logger: logger}
}

func (p *Producer) Publish(ctx context.Context, key string, value []byte) error {
	msg := kafka.Message{
		Key:   []byte(key),
		Value: value,
	}

	err := p.writer.WriteMessages(ctx, msg)
	if err != nil {
		p.logger.Error("failed to publish message",
			zap.String("key", key),
			zap.Error(err),
		)
		return err
	}

	p.logger.Debug("published message", zap.String("key", key), zap.Int("size", len(value)))
	return nil
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
