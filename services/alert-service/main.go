package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
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
	rules := []models.AlertRule{
		{Symbol: "BTC", Threshold: 65000, Direction: "above"},
		{Symbol: "BTC", Threshold: 60000, Direction: "below"},
		{Symbol: "ETH", Threshold: 3500, Direction: "above"},
		{Symbol: "ETH", Threshold: 3000, Direction: "below"},
		{Symbol: "SOL", Threshold: 160, Direction: "above"},
		{Symbol: "SOL", Threshold: 130, Direction: "below"},
	}

	engine := domain.NewAlertEngine(rules)

	consumer := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAgg,
		"alert-service-group",
		log,
	)

	producer := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopicAlerts, log)
	defer producer.Close()

	// Metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		log.Info("metrics server starting", zap.String("addr", ":9092"))
		http.ListenAndServe(":9092", mux)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutting down alert service")
		cancel()
	}()

	log.Info("alert service started", zap.Int("rules", len(rules)))

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
