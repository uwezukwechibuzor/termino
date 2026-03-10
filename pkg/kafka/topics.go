package kafka

import (
	"context"
	"net"
	"strconv"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// EnsureTopics creates Kafka topics if they don't exist.
func EnsureTopics(ctx context.Context, brokers []string, logger *zap.Logger, topics ...string) error {
	conn, err := kafkago.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return err
	}

	controllerConn, err := kafkago.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		return err
	}
	defer controllerConn.Close()

	topicConfigs := make([]kafkago.TopicConfig, len(topics))
	for i, t := range topics {
		topicConfigs[i] = kafkago.TopicConfig{
			Topic:             t,
			NumPartitions:     6, // partition by symbol
			ReplicationFactor: 1, // set to 3 in production
		}
		logger.Info("ensuring topic", zap.String("topic", t))
	}

	return controllerConn.CreateTopics(topicConfigs...)
}
