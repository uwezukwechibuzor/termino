package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/uwezukwechibuzor/termino/internal/domain"
	"github.com/uwezukwechibuzor/termino/pkg/config"
	"github.com/uwezukwechibuzor/termino/pkg/kafka"
	"github.com/uwezukwechibuzor/termino/pkg/logger"
	"github.com/uwezukwechibuzor/termino/pkg/models"
	"go.uber.org/zap"
)

var (
	alertsTriggered = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alerts_triggered_total",
			Help: "Total alerts triggered",
		},
		[]string{"symbol", "direction"},
	)
	ruleIDCounter atomic.Int64
)

func init() {
	prometheus.MustRegister(alertsTriggered)
}

func main() {
	log := logger.New("alert-service")
	defer log.Sync()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Default alert rules
	defaultRules := []models.AlertRule{
		{ID: "default-1", Symbol: "BTC", Threshold: 65000, Direction: "above"},
		{ID: "default-2", Symbol: "BTC", Threshold: 60000, Direction: "below"},
		{ID: "default-3", Symbol: "ETH", Threshold: 3500, Direction: "above"},
		{ID: "default-4", Symbol: "ETH", Threshold: 3000, Direction: "below"},
		{ID: "default-5", Symbol: "SOL", Threshold: 160, Direction: "above"},
		{ID: "default-6", Symbol: "SOL", Threshold: 130, Direction: "below"},
	}

	engine := domain.NewAlertEngine(defaultRules)

	consumer := kafka.NewConsumerWithDLQ(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAgg,
		"alert-service-group",
		cfg.KafkaTopicDLQ,
		cfg.DLQMaxRetries,
		log,
	)

	producer := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopicAlerts, log)
	defer producer.Close()

	// HTTP server with metrics + alert rules API
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		// Alert rules CRUD API
		mux.HandleFunc("/rules", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				handleListRules(w, engine)
			case http.MethodPost:
				handleAddRule(w, r, engine, log)
			case http.MethodDelete:
				handleDeleteRule(w, r, engine, log)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		})

		addr := ":" + cfg.AlertHTTPPort
		log.Info("alert API server starting", zap.String("addr", addr))
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatal("alert API server failed", zap.Error(err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutting down alert service")
		cancel()
	}()

	log.Info("alert service started", zap.Int("default_rules", len(defaultRules)))

	consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		price, err := models.UnmarshalAggregatedPrice(msg.Value)
		if err != nil {
			return err
		}

		alerts := engine.Evaluate(*price)
		for _, alert := range alerts {
			data, err := alert.Marshal()
			if err != nil {
				log.Error("failed to marshal alert", zap.Error(err))
				continue
			}

			if err := producer.Publish(ctx, alert.Symbol, data); err != nil {
				log.Error("failed to publish alert", zap.Error(err))
				continue
			}

			alertsTriggered.WithLabelValues(alert.Symbol, alert.Direction).Inc()
			log.Warn("ALERT TRIGGERED",
				zap.String("symbol", alert.Symbol),
				zap.String("rule", alert.Rule),
				zap.Float64("current", alert.Current),
				zap.String("message", alert.Message),
			)
		}

		return nil
	})
}

func handleListRules(w http.ResponseWriter, engine *domain.AlertEngine) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(engine.ListRules())
}

func handleAddRule(w http.ResponseWriter, r *http.Request, engine *domain.AlertEngine, log *zap.Logger) {
	var rule models.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if rule.Symbol == "" || rule.Direction == "" || rule.Threshold == 0 {
		http.Error(w, "symbol, threshold, and direction are required", http.StatusBadRequest)
		return
	}

	if rule.Direction != "above" && rule.Direction != "below" {
		http.Error(w, "direction must be 'above' or 'below'", http.StatusBadRequest)
		return
	}

	if rule.ID == "" {
		rule.ID = fmt.Sprintf("rule-%d", ruleIDCounter.Add(1))
	}

	engine.AddRule(rule)
	log.Info("alert rule added",
		zap.String("id", rule.ID),
		zap.String("symbol", rule.Symbol),
		zap.Float64("threshold", rule.Threshold),
		zap.String("direction", rule.Direction),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

func handleDeleteRule(w http.ResponseWriter, r *http.Request, engine *domain.AlertEngine, log *zap.Logger) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id query parameter is required", http.StatusBadRequest)
		return
	}

	if engine.RemoveRule(id) {
		log.Info("alert rule removed", zap.String("id", id))
		w.WriteHeader(http.StatusNoContent)
	} else {
		http.Error(w, "rule not found", http.StatusNotFound)
	}
}
