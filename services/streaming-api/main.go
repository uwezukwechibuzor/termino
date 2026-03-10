package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/uwezukwechibuzor/termino/pkg/config"
	"github.com/uwezukwechibuzor/termino/pkg/kafka"
	"github.com/uwezukwechibuzor/termino/pkg/logger"
	"github.com/uwezukwechibuzor/termino/pkg/models"
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
	logger  *zap.Logger
	mu      sync.RWMutex
	clients map[chan []byte]map[string]bool // channel -> subscribed symbols
	latest  map[string]models.AggregatedPrice
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
	}

	// Consume aggregated prices from Kafka
	consumer := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAgg,
		"streaming-api-group",
		log,
	)

	// Also consume alerts
	alertConsumer := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAlerts,
		"streaming-api-alerts-group",
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

		server.broadcast(price.Symbol, msg.Value)
		return nil
	})

	go alertConsumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		server.broadcastAlert(msg.Value)
		return nil
	})

	// HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", server.handleStream)
	mux.HandleFunc("/stream/alerts", server.handleAlertStream)
	mux.HandleFunc("/prices", server.handleLatestPrices)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())

	httpServer := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      mux,
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

func (s *StreamingServer) handleLatestPrices(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.latest)
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
