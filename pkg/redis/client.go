package redis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/uwezukwechibuzor/termino/pkg/models"
	"go.uber.org/zap"
)

const latestPricesKey = "latest_prices"

type Client struct {
	rdb    *redis.Client
	logger *zap.Logger
}

func NewClient(addr string, logger *zap.Logger) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	logger.Info("connected to Redis", zap.String("addr", addr))
	return &Client{rdb: rdb, logger: logger}, nil
}

// SetPrice caches a single symbol's aggregated price.
func (c *Client) SetPrice(ctx context.Context, symbol string, price models.AggregatedPrice, ttl time.Duration) error {
	data, err := json.Marshal(price)
	if err != nil {
		return err
	}
	return c.rdb.HSet(ctx, latestPricesKey, symbol, data).Err()
}

// SetPricesTTL sets the TTL on the prices hash.
func (c *Client) SetPricesTTL(ctx context.Context, ttl time.Duration) error {
	return c.rdb.Expire(ctx, latestPricesKey, ttl).Err()
}

// GetPrices returns all cached prices.
func (c *Client) GetPrices(ctx context.Context) (map[string]models.AggregatedPrice, error) {
	result, err := c.rdb.HGetAll(ctx, latestPricesKey).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, nil
	}

	prices := make(map[string]models.AggregatedPrice, len(result))
	for symbol, data := range result {
		var p models.AggregatedPrice
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			c.logger.Warn("failed to unmarshal cached price", zap.String("symbol", symbol), zap.Error(err))
			continue
		}
		prices[symbol] = p
	}
	return prices, nil
}

// GetPrice returns a single cached price.
func (c *Client) GetPrice(ctx context.Context, symbol string) (*models.AggregatedPrice, error) {
	data, err := c.rdb.HGet(ctx, latestPricesKey, symbol).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var p models.AggregatedPrice
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) Close() error {
	return c.rdb.Close()
}
