package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	KafkaBrokers     []string
	KafkaTopicPrices string
	KafkaTopicAgg    string
	KafkaTopicAlerts string
	PostgresDSN      string
	RedisAddr        string
	HTTPPort         string
	PrometheusPort   string
	PollInterval     time.Duration
	Symbols          []string
	LogLevel         string
}

func Load() *Config {
	return &Config{
		KafkaBrokers:     strings.Split(getEnv("KAFKA_BROKERS", "localhost:9092"), ","),
		KafkaTopicPrices: getEnv("KAFKA_TOPIC_PRICES", "price-events"),
		KafkaTopicAgg:    getEnv("KAFKA_TOPIC_AGG", "aggregated-prices"),
		KafkaTopicAlerts: getEnv("KAFKA_TOPIC_ALERTS", "price-alerts"),
		PostgresDSN:      getEnv("POSTGRES_DSN", "postgres://crypto:crypto@localhost:5432/cryptodb?sslmode=disable"),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		HTTPPort:         getEnv("HTTP_PORT", "8080"),
		PrometheusPort:   getEnv("PROMETHEUS_PORT", "9090"),
		PollInterval:     getDurationEnv("POLL_INTERVAL", 5*time.Second),
		Symbols:          strings.Split(getEnv("SYMBOLS", "BTC,ETH,SOL,ADA,DOT,AVAX,MATIC,LINK"), ","),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return fallback
}
