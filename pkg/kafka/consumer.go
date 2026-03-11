package kafka

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type MessageHandler func(ctx context.Context, msg kafka.Message) error

type Consumer struct {
	reader      *kafka.Reader
	logger      *zap.Logger
	handler     MessageHandler
	dlqProducer *Producer
	maxRetries  int
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

// NewConsumerWithDLQ creates a consumer with dead letter queue support.
func NewConsumerWithDLQ(brokers []string, topic, groupID, dlqTopic string, maxRetries int, logger *zap.Logger) *Consumer {
	c := NewConsumer(brokers, topic, groupID, logger)
	c.dlqProducer = NewProducer(brokers, dlqTopic, logger)
	c.maxRetries = maxRetries
	logger.Info("DLQ enabled",
		zap.String("topic", topic),
		zap.String("dlq_topic", dlqTopic),
		zap.Int("max_retries", maxRetries),
	)
	return c
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
				c.handleFailure(ctx, msg, err)
				// Commit the failed message so we don't reprocess it endlessly
				if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
					c.logger.Error("failed to commit failed message offset", zap.Error(commitErr))
				}
				continue
			}

			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				c.logger.Error("failed to commit offset", zap.Error(err))
			}
		}
	}
}

func (c *Consumer) handleFailure(ctx context.Context, msg kafka.Message, handlerErr error) {
	retryCount := getRetryCount(msg.Headers)

	if c.dlqProducer == nil {
		c.logger.Error("failed to handle message (no DLQ configured)",
			zap.String("key", string(msg.Key)),
			zap.Error(handlerErr),
		)
		return
	}

	if retryCount < c.maxRetries {
		newRetry := retryCount + 1
		c.logger.Warn("retrying failed message",
			zap.String("key", string(msg.Key)),
			zap.Int("retry", newRetry),
			zap.Int("max_retries", c.maxRetries),
			zap.Error(handlerErr),
		)

		headers := updateRetryHeader(msg.Headers, newRetry)
		err := c.dlqProducer.writer.WriteMessages(ctx, kafka.Message{
			Topic:   c.reader.Config().Topic,
			Key:     msg.Key,
			Value:   msg.Value,
			Headers: headers,
		})
		if err != nil {
			c.logger.Error("failed to re-publish for retry, sending to DLQ",
				zap.String("key", string(msg.Key)),
				zap.Error(err),
			)
			c.sendToDLQ(ctx, msg, handlerErr, retryCount)
		}
		return
	}

	c.sendToDLQ(ctx, msg, handlerErr, retryCount)
}

func (c *Consumer) sendToDLQ(ctx context.Context, msg kafka.Message, handlerErr error, retryCount int) {
	c.logger.Error("sending message to DLQ",
		zap.String("key", string(msg.Key)),
		zap.String("original_topic", c.reader.Config().Topic),
		zap.Int("retry_count", retryCount),
		zap.Error(handlerErr),
	)

	headers := []kafka.Header{
		{Key: "original-topic", Value: []byte(c.reader.Config().Topic)},
		{Key: "original-group", Value: []byte(c.reader.Config().GroupID)},
		{Key: "error-message", Value: []byte(handlerErr.Error())},
		{Key: "retry-count", Value: []byte(strconv.Itoa(retryCount))},
		{Key: "failed-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	}

	dlqMsg := kafka.Message{
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	}

	if err := c.dlqProducer.writer.WriteMessages(ctx, dlqMsg); err != nil {
		c.logger.Error("CRITICAL: failed to send message to DLQ",
			zap.String("key", string(msg.Key)),
			zap.Error(err),
		)
	}
}

func getRetryCount(headers []kafka.Header) int {
	for _, h := range headers {
		if h.Key == "retry-count" {
			if count, err := strconv.Atoi(string(h.Value)); err == nil {
				return count
			}
		}
	}
	return 0
}

func updateRetryHeader(headers []kafka.Header, count int) []kafka.Header {
	found := false
	result := make([]kafka.Header, 0, len(headers)+1)
	for _, h := range headers {
		if h.Key == "retry-count" {
			result = append(result, kafka.Header{
				Key:   "retry-count",
				Value: []byte(fmt.Sprintf("%d", count)),
			})
			found = true
		} else {
			result = append(result, h)
		}
	}
	if !found {
		result = append(result, kafka.Header{
			Key:   "retry-count",
			Value: []byte(fmt.Sprintf("%d", count)),
		})
	}
	return result
}

func (c *Consumer) Close() error {
	if c.dlqProducer != nil {
		c.dlqProducer.Close()
	}
	return c.reader.Close()
}
