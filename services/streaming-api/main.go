package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/uwezukwechibuzor/termino/pkg/config"
	"github.com/uwezukwechibuzor/termino/pkg/kafka"
	"github.com/uwezukwechibuzor/termino/pkg/logger"
	"github.com/uwezukwechibuzor/termino/pkg/middleware"
	"github.com/uwezukwechibuzor/termino/pkg/models"
	redisclient "github.com/uwezukwechibuzor/termino/pkg/redis"
	"go.uber.org/zap"
)

var (
	activeClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "streaming_active_clients",
			Help: "Number of active SSE clients",
		},
	)
	messagesStreamed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "messages_streamed_total",
			Help: "Total messages streamed to clients",
		},
	)
)

func init() {
	prometheus.MustRegister(activeClients, messagesStreamed)
}

type StreamingServer struct {
	logger *zap.Logger
	mu     sync.RWMutex
	clients map[chan []byte]map[string]bool // channel -> subscribed symbols
	latest  map[string]models.AggregatedPrice
	redis   *redisclient.Client
	db      *sql.DB
	cfg     *config.Config
}

func main() {
	log := logger.New("streaming-api")
	defer log.Sync()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &StreamingServer{
		logger:  log,
		clients: make(map[chan []byte]map[string]bool),
		latest:  make(map[string]models.AggregatedPrice),
		cfg:     cfg,
	}

	// Connect to Redis
	rc, err := redisclient.NewClient(cfg.RedisAddr, log)
	if err != nil {
		log.Warn("Redis unavailable, falling back to in-memory cache", zap.Error(err))
	} else {
		server.redis = rc
		defer rc.Close()
	}

	// Connect to PostgreSQL for history queries
	db, err := sql.Open("postgres", cfg.PostgresDSN)
	if err != nil {
		log.Warn("PostgreSQL unavailable, history endpoint disabled", zap.Error(err))
	} else {
		db.SetMaxOpenConns(5)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(5 * time.Minute)
		if err := db.Ping(); err != nil {
			log.Warn("PostgreSQL ping failed, history endpoint disabled", zap.Error(err))
			db.Close()
		} else {
			server.db = db
			defer db.Close()
			log.Info("connected to PostgreSQL for history queries")
		}
	}

	// Ensure DLQ topic exists
	if err := kafka.EnsureTopics(ctx, cfg.KafkaBrokers, log, cfg.KafkaTopicDLQ); err != nil {
		log.Warn("could not ensure DLQ topic", zap.Error(err))
	}

	// Consume aggregated prices from Kafka
	consumer := kafka.NewConsumerWithDLQ(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAgg,
		"streaming-api-group",
		cfg.KafkaTopicDLQ,
		cfg.DLQMaxRetries,
		log,
	)

	// Also consume alerts
	alertConsumer := kafka.NewConsumerWithDLQ(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAlerts,
		"streaming-api-alerts-group",
		cfg.KafkaTopicDLQ,
		cfg.DLQMaxRetries,
		log,
	)

	go consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		price, err := models.UnmarshalAggregatedPrice(msg.Value)
		if err != nil {
			return err
		}

		server.mu.Lock()
		server.latest[price.Symbol] = *price
		server.mu.Unlock()

		// Cache in Redis
		if server.redis != nil {
			if err := server.redis.SetPrice(ctx, price.Symbol, *price, cfg.RedisTTL); err != nil {
				log.Warn("failed to cache price in Redis", zap.Error(err))
			}
		}

		server.broadcast(price.Symbol, msg.Value)
		return nil
	})

	go alertConsumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		server.broadcastAlert(msg.Value)
		return nil
	})

	// HTTP server with rate limiting and SSE client limits
	mux := http.NewServeMux()

	sseLimit := middleware.MaxSSEClients(cfg.MaxSSEClients)
	mux.HandleFunc("/stream", sseLimit(server.handleStream))
	mux.HandleFunc("/stream/alerts", sseLimit(server.handleAlertStream))
	mux.HandleFunc("/prices", server.handleLatestPrices)
	mux.HandleFunc("/history", server.handleHistory)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())

	// Wrap with rate limiter
	rateLimiter := middleware.NewRateLimiter(cfg.RateLimitPerSec, cfg.RateLimitBurst)
	handler := rateLimiter.Middleware(mux)

	httpServer := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 0, // SSE requires no write timeout
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("streaming API server starting", zap.String("addr", httpServer.Addr))
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("shutting down streaming API")
	cancel()
	httpServer.Shutdown(context.Background())
}

// handleStream serves Server-Sent Events for price updates.
// Query: /stream?symbols=BTC,ETH,SOL
func (s *StreamingServer) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	symbols := parseSymbols(r.URL.Query().Get("symbols"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 64)
	symMap := make(map[string]bool)
	for _, sym := range symbols {
		symMap[strings.ToUpper(sym)] = true
	}

	s.mu.Lock()
	s.clients[ch] = symMap
	s.mu.Unlock()
	activeClients.Inc()

	s.logger.Info("client connected",
		zap.Strings("symbols", symbols),
		zap.String("remote", r.RemoteAddr),
	)

	// Send latest prices immediately
	s.mu.RLock()
	for sym, price := range s.latest {
		if len(symMap) == 0 || symMap[sym] {
			data, _ := json.Marshal(price)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}
	s.mu.RUnlock()
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			s.removeClient(ch)
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			messagesStreamed.Inc()
		}
	}
}

func (s *StreamingServer) handleAlertStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 64)
	// Empty symbol map means subscribe to all
	s.mu.Lock()
	s.clients[ch] = map[string]bool{"__alerts__": true}
	s.mu.Unlock()
	activeClients.Inc()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			s.removeClient(ch)
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: alert\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleLatestPrices returns the latest prices, using Redis cache with in-memory fallback.
func (s *StreamingServer) handleLatestPrices(w http.ResponseWriter, r *http.Request) {
	// Try Redis first
	if s.redis != nil {
		prices, err := s.redis.GetPrices(r.Context())
		if err == nil && len(prices) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			json.NewEncoder(w).Encode(prices)
			return
		}
	}

	// Fallback to in-memory
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	json.NewEncoder(w).Encode(s.latest)
}

// handleHistory returns historical price data from PostgreSQL.
// Query: /history?symbol=BTC&limit=50
func (s *StreamingServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "history not available: database not connected", http.StatusServiceUnavailable)
		return
	}

	symbol := strings.ToUpper(r.URL.Query().Get("symbol"))
	if symbol == "" {
		http.Error(w, "symbol query parameter is required", http.StatusBadRequest)
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	rows, err := s.db.QueryContext(r.Context(),
		`SELECT symbol, price, COALESCE(avg_price, 0), COALESCE(high_price, 0),
		        COALESCE(low_price, 0), COALESCE(change_pct, 0), COALESCE(volume, 0),
		        EXTRACT(EPOCH FROM timestamp)::bigint
		 FROM prices WHERE symbol = $1 ORDER BY timestamp DESC LIMIT $2`,
		symbol, limit,
	)
	if err != nil {
		s.logger.Error("history query failed", zap.Error(err))
		http.Error(w, "database query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var prices []models.AggregatedPrice
	for rows.Next() {
		var p models.AggregatedPrice
		if err := rows.Scan(&p.Symbol, &p.Price, &p.AvgPrice, &p.HighPrice,
			&p.LowPrice, &p.ChangePct, &p.Volume, &p.Timestamp); err != nil {
			s.logger.Error("failed to scan history row", zap.Error(err))
			continue
		}
		prices = append(prices, p)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prices)
}

func (s *StreamingServer) broadcast(symbol string, data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for ch, syms := range s.clients {
		if syms["__alerts__"] {
			continue
		}
		if len(syms) == 0 || syms[symbol] {
			select {
			case ch <- data:
			default:
				// Client too slow, skip
			}
		}
	}
}

func (s *StreamingServer) broadcastAlert(data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for ch, syms := range s.clients {
		if syms["__alerts__"] {
			select {
			case ch <- data:
			default:
			}
		}
	}
}

func (s *StreamingServer) removeClient(ch chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, ch)
	close(ch)
	activeClients.Dec()
	s.logger.Info("client disconnected")
}

func parseSymbols(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToUpper(p))
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
