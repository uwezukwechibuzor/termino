package kafka

import (
	"context"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type MessageHandler func(ctx context.Context, msg kafka.Message) error

type Consumer struct {
	reader  *kafka.Reader
	logger  *zap.Logger
	handler MessageHandler
}

func NewConsumer(brokers []string, topic, groupID string, logger *zap.Logger) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6, // 10MB
		CommitInterval: 1000, // commit offsets every second
		StartOffset:    kafka.LastOffset,
	})

	return &Consumer{reader: r, logger: logger}
}

func (c *Consumer) Consume(ctx context.Context, handler MessageHandler) error {
	c.handler = handler
	c.logger.Info("starting consumer",
		zap.String("topic", c.reader.Config().Topic),
		zap.String("group", c.reader.Config().GroupID),
	)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("consumer context cancelled, shutting down")
			return c.reader.Close()
		default:
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				c.logger.Error("failed to fetch message", zap.Error(err))
				continue
			}

			if err := handler(ctx, msg); err != nil {
				c.logger.Error("failed to handle message",
					zap.String("key", string(msg.Key)),
					zap.Error(err),
				)
				// Dead letter queue would go here in production
				continue
			}

			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				c.logger.Error("failed to commit offset", zap.Error(err))
			}
		}
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
