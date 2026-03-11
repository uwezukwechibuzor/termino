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
	KafkaTopicDLQ    string
	PostgresDSN      string
	RedisAddr        string
	RedisTTL         time.Duration
	HTTPPort         string
	PrometheusPort   string
	AlertHTTPPort    string
	PollInterval     time.Duration
	Symbols          []string
	LogLevel         string
	RateLimitPerSec  float64
	RateLimitBurst   int
	MaxSSEClients    int
	DLQMaxRetries    int
}

func Load() *Config {
	return &Config{
		KafkaBrokers:     strings.Split(getEnv("KAFKA_BROKERS", "localhost:9092"), ","),
		KafkaTopicPrices: getEnv("KAFKA_TOPIC_PRICES", "price-events"),
		KafkaTopicAgg:    getEnv("KAFKA_TOPIC_AGG", "aggregated-prices"),
		KafkaTopicAlerts: getEnv("KAFKA_TOPIC_ALERTS", "price-alerts"),
		KafkaTopicDLQ:    getEnv("KAFKA_TOPIC_DLQ", "dead-letter-queue"),
		PostgresDSN:      getEnv("POSTGRES_DSN", "postgres://crypto:crypto@localhost:5432/cryptodb?sslmode=disable"),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		RedisTTL:         getDurationEnv("REDIS_TTL", 30*time.Second),
		HTTPPort:         getEnv("HTTP_PORT", "8080"),
		PrometheusPort:   getEnv("PROMETHEUS_PORT", "9090"),
		AlertHTTPPort:    getEnv("ALERT_HTTP_PORT", "8081"),
		PollInterval:     getDurationEnv("POLL_INTERVAL", 5*time.Second),
		Symbols:          strings.Split(getEnv("SYMBOLS", "BTC,ETH,SOL,ADA,DOT,AVAX,MATIC,LINK"), ","),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		RateLimitPerSec:  getFloatEnv("RATE_LIMIT_PER_SEC", 10),
		RateLimitBurst:   getIntEnv("RATE_LIMIT_BURST", 20),
		MaxSSEClients:    getIntEnv("MAX_SSE_CLIENTS", 100),
		DLQMaxRetries:    getIntEnv("DLQ_MAX_RETRIES", 3),
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

func getIntEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getFloatEnv(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
