package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uwezukwechibuzor/termino/pkg/config"
	"github.com/uwezukwechibuzor/termino/pkg/kafka"
	"github.com/uwezukwechibuzor/termino/pkg/logger"
	"github.com/uwezukwechibuzor/termino/pkg/models"
	"go.uber.org/zap"
)

var (
	pricesProduced = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prices_produced_total",
			Help: "Total number of price events produced",
		},
		[]string{"symbol", "exchange"},
	)
	apiLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_latency_seconds",
			Help:    "API call latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"exchange"},
	)
	apiErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_errors_total",
			Help: "Total API errors",
		},
		[]string{"exchange"},
	)
)

func init() {
	prometheus.MustRegister(pricesProduced, apiLatency, apiErrors)
}

// CoinGecko API response structure
type coinGeckoResponse map[string]map[string]float64

// symbolToCoinGeckoID maps trading symbols to CoinGecko IDs.
var symbolToCoinGeckoID = map[string]string{
	"BTC":   "bitcoin",
	"ETH":   "ethereum",
	"SOL":   "solana",
	"ADA":   "cardano",
	"DOT":   "polkadot",
	"AVAX":  "avalanche-2",
	"MATIC": "matic-network",
	"LINK":  "chainlink",
}

type PriceProducer struct {
	cfg      *config.Config
	producer *kafka.Producer
	logger   *zap.Logger
	client   *http.Client
	mu       sync.Mutex
	cache    map[string]float64 // last known prices for fallback
}

func main() {
	log := logger.New("price-producer")
	defer log.Sync()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ensure topics exist
	if err := kafka.EnsureTopics(ctx, cfg.KafkaBrokers, log,
		cfg.KafkaTopicPrices, cfg.KafkaTopicAgg, cfg.KafkaTopicAlerts,
	); err != nil {
		log.Warn("could not ensure topics (may already exist)", zap.Error(err))
	}

	producer := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopicPrices, log)
	defer producer.Close()

	pp := &PriceProducer{
		cfg:      cfg,
		producer: producer,
		logger:   log,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache: make(map[string]float64),
	}

	// Metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		addr := ":" + cfg.PrometheusPort
		log.Info("metrics server starting", zap.String("addr", addr))
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatal("metrics server failed", zap.Error(err))
		}
	}()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Info("shutting down price producer")
		cancel()
	}()

	pp.run(ctx)
}

func (pp *PriceProducer) run(ctx context.Context) {
	ticker := time.NewTicker(pp.cfg.PollInterval)
	defer ticker.Stop()

	pp.logger.Info("price producer started",
		zap.Strings("symbols", pp.cfg.Symbols),
		zap.Duration("interval", pp.cfg.PollInterval),
	)

	// Initial fetch
	pp.fetchAndPublish(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pp.fetchAndPublish(ctx)
		}
	}
}

func (pp *PriceProducer) fetchAndPublish(ctx context.Context) {
	prices, err := pp.fetchFromCoinGecko(ctx)
	if err != nil {
		pp.logger.Warn("CoinGecko fetch failed, using simulated prices", zap.Error(err))
		apiErrors.WithLabelValues("coingecko").Inc()
		prices = pp.simulatePrices()
	}

	for _, price := range prices {
		data, err := price.Marshal()
		if err != nil {
			pp.logger.Error("failed to marshal price event", zap.Error(err))
			continue
		}

		if err := pp.producer.Publish(ctx, price.Symbol, data); err != nil {
			pp.logger.Error("failed to publish price",
				zap.String("symbol", price.Symbol),
				zap.Error(err),
			)
			continue
		}

		pricesProduced.WithLabelValues(price.Symbol, price.Exchange).Inc()
		pp.logger.Info("published price",
			zap.String("symbol", price.Symbol),
			zap.Float64("price", price.Price),
			zap.String("exchange", price.Exchange),
		)
	}
}

func (pp *PriceProducer) fetchFromCoinGecko(ctx context.Context) ([]models.PriceEvent, error) {
	ids := ""
	for _, sym := range pp.cfg.Symbols {
		if id, ok := symbolToCoinGeckoID[sym]; ok {
			if ids != "" {
				ids += ","
			}
			ids += id
		}
	}

	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd&include_24hr_vol=true", ids)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := pp.client.Do(req)
	apiLatency.WithLabelValues("coingecko").Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CoinGecko API returned %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	idToSymbol := make(map[string]string)
	for sym, id := range symbolToCoinGeckoID {
		idToSymbol[id] = sym
	}

	var events []models.PriceEvent
	pp.mu.Lock()
	defer pp.mu.Unlock()

	for id, data := range result {
		sym, ok := idToSymbol[id]
		if !ok {
			continue
		}
		price := data["usd"]
		vol := data["usd_24h_vol"]

		pp.cache[sym] = price

		events = append(events, models.PriceEvent{
			Symbol:    sym,
			Price:     price,
			Exchange:  "coingecko",
			Volume:    vol,
			Timestamp: models.NowUnix(),
		})
	}

	return events, nil
}

// simulatePrices generates realistic price data when APIs are unavailable.
func (pp *PriceProducer) simulatePrices() []models.PriceEvent {
	basePrices := map[string]float64{
		"BTC":   64000,
		"ETH":   3200,
		"SOL":   145,
		"ADA":   0.45,
		"DOT":   7.5,
		"AVAX":  35,
		"MATIC": 0.85,
		"LINK":  15,
	}

	pp.mu.Lock()
	defer pp.mu.Unlock()

	var events []models.PriceEvent
	for _, sym := range pp.cfg.Symbols {
		base, ok := basePrices[sym]
		if !ok {
			continue
		}

		if cached, ok := pp.cache[sym]; ok {
			base = cached
		}

		// Random walk: ±0.5%
		change := base * (rand.Float64()*0.01 - 0.005)
		price := base + change
		pp.cache[sym] = price

		events = append(events, models.PriceEvent{
			Symbol:    sym,
			Price:     price,
			Exchange:  "simulated",
			Volume:    rand.Float64() * 1000000,
			Timestamp: models.NowUnix(),
		})
	}
	return events
}
