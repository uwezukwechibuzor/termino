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
	pricesConsumed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prices_consumed_total",
			Help: "Total price events consumed by aggregator",
		},
		[]string{"symbol"},
	)
	aggregationsPublished = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "aggregations_published_total",
			Help: "Total aggregated prices published",
		},
	)
)

func init() {
	prometheus.MustRegister(pricesConsumed, aggregationsPublished)
}

func main() {
	log := logger.New("price-aggregator")
	defer log.Sync()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	consumer := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicPrices,
		"price-aggregator-group",
		log,
	)

	producer := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopicAgg, log)
	defer producer.Close()

	aggregator := domain.NewPriceAggregator(100) // 100-event window

	// Metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		log.Info("metrics server starting", zap.String("addr", ":9091"))
		http.ListenAndServe(":9091", mux)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutting down aggregator")
		cancel()
	}()

	log.Info("price aggregator started")

	consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		event, err := models.UnmarshalPriceEvent(msg.Value)
		if err != nil {
			log.Error("failed to unmarshal price event", zap.Error(err))
			return err
		}

		pricesConsumed.WithLabelValues(event.Symbol).Inc()

		agg := aggregator.Add(*event)

		data, err := agg.Marshal()
		if err != nil {
			return err
		}

		if err := producer.Publish(ctx, agg.Symbol, data); err != nil {
			return err
		}

		aggregationsPublished.Inc()

		log.Debug("aggregated price published",
			zap.String("symbol", agg.Symbol),
			zap.Float64("price", agg.Price),
			zap.Float64("avg", agg.AvgPrice),
			zap.Float64("change_pct", agg.ChangePct),
		)

		return nil
	})
}
