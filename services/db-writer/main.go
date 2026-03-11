package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/uwezukwechibuzor/termino/pkg/models"
	"go.uber.org/zap"
)

var (
	rowsInserted = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "db_rows_inserted_total",
			Help: "Total rows inserted into database",
		},
	)
	batchesWritten = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "db_batches_written_total",
			Help: "Total batches written to database",
		},
	)
)

func init() {
	prometheus.MustRegister(rowsInserted, batchesWritten)
}

type DBWriter struct {
	db     *sql.DB
	logger *zap.Logger
	mu     sync.Mutex
	batch  []models.AggregatedPrice
}

func main() {
	log := logger.New("db-writer")
	defer log.Sync()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := sql.Open("postgres", cfg.PostgresDSN)
	if err != nil {
		log.Fatal("failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := initSchema(db); err != nil {
		log.Fatal("failed to initialize schema", zap.Error(err))
	}

	writer := &DBWriter{
		db:     db,
		logger: log,
		batch:  make([]models.AggregatedPrice, 0, 100),
	}

	consumer := kafka.NewConsumerWithDLQ(
		cfg.KafkaBrokers,
		cfg.KafkaTopicAgg,
		"db-writer-group",
		cfg.KafkaTopicDLQ,
		cfg.DLQMaxRetries,
		log,
	)

	// Flush batch periodically
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				writer.flush(ctx)
				return
			case <-ticker.C:
				writer.flush(ctx)
			}
		}
	}()

	// Metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		log.Info("metrics server starting", zap.String("addr", ":9093"))
		http.ListenAndServe(":9093", mux)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutting down db writer")
		cancel()
	}()

	log.Info("db writer started")

	consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		price, err := models.UnmarshalAggregatedPrice(msg.Value)
		if err != nil {
			return err
		}

		writer.addToBatch(*price)
		return nil
	})
}

func (w *DBWriter) addToBatch(price models.AggregatedPrice) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.batch = append(w.batch, price)

	if len(w.batch) >= 100 {
		w.flushLocked(context.Background())
	}
}

func (w *DBWriter) flush(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked(ctx)
}

func (w *DBWriter) flushLocked(ctx context.Context) {
	if len(w.batch) == 0 {
		return
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		w.logger.Error("failed to begin transaction", zap.Error(err))
		return
	}

	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO prices (symbol, price, avg_price, high_price, low_price, change_pct, volume, timestamp) VALUES ($1, $2, $3, $4, $5, $6, $7, to_timestamp($8)) ON CONFLICT DO NOTHING")
	if err != nil {
		tx.Rollback()
		w.logger.Error("failed to prepare statement", zap.Error(err))
		return
	}
	defer stmt.Close()

	count := 0
	for _, p := range w.batch {
		_, err := stmt.ExecContext(ctx,
			p.Symbol, p.Price, p.AvgPrice, p.HighPrice, p.LowPrice, p.ChangePct, p.Volume, p.Timestamp,
		)
		if err != nil {
			w.logger.Error("failed to insert row", zap.String("symbol", p.Symbol), zap.Error(err))
			continue
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		w.logger.Error("failed to commit transaction", zap.Error(err))
		return
	}

	rowsInserted.Add(float64(count))
	batchesWritten.Inc()
	w.logger.Info("batch written", zap.Int("rows", count))
	w.batch = w.batch[:0]
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS prices (
			id BIGSERIAL PRIMARY KEY,
			symbol VARCHAR(10) NOT NULL,
			price DOUBLE PRECISION NOT NULL,
			avg_price DOUBLE PRECISION,
			high_price DOUBLE PRECISION,
			low_price DOUBLE PRECISION,
			change_pct DOUBLE PRECISION,
			volume DOUBLE PRECISION,
			timestamp TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(symbol, timestamp)
		);
		CREATE INDEX IF NOT EXISTS idx_prices_symbol_ts ON prices(symbol, timestamp DESC);
	`)
	return err
}
