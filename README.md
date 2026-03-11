# Termino

A terminal-based real-time cryptocurrency price streaming system built with Go and Apache Kafka.

Termino streams live crypto prices from exchanges, processes them through an event-driven Kafka pipeline, and delivers real-time feeds to a polished terminal UI.

```
crypto-stream prices BTC ETH SOL

 CRYPTO PRICE STREAM

 SYMBOL   PRICE          AVG            HIGH           LOW            CHANGE %    VOLUME         UPDATED
 BTC      $64,012.22     $63,998.50     $64,150.00     $63,800.00     +0.35%      1.24B          14:32:05
 ETH      $3,211.11      $3,208.44      $3,225.00      $3,195.00      -0.12%      892.50M        14:32:05
 SOL      $145.67        $145.20        $146.80        $144.10        +1.02%      345.20M        14:32:05

 Symbols: BTC, ETH, SOL | Last update: 14:32:05 | ● CONNECTED
```

---

<img width="1019" height="532" alt="Screenshot 2026-03-11 at 14 56 47" src="https://github.com/user-attachments/assets/2c2442b5-2baf-483d-aaa7-0383a821e6a0" />


<img width="1385" height="790" alt="Screenshot 2026-03-11 at 14 58 04" src="https://github.com/user-attachments/assets/9ad49962-72c3-4d0b-9fb7-54a9e97f9eca" />


## System Architecture

```
                    ┌──────────────────────────────────────────────────────┐
                    │                    TERMINO SYSTEM                    │
                    └──────────────────────────────────────────────────────┘

    ┌─────────────┐
    │  CoinGecko  │───┐
    │  Binance    │   │
    │  (Exchange  │   │     Rate-limited polling (every 5s)
    │   APIs)     │   │
    └─────────────┘   │
                      ▼
              ┌───────────────┐
              │    PRICE      │     Fetches prices, normalizes data,
              │   PRODUCER    │     publishes to Kafka with symbol
              │   SERVICE     │     as partition key.
              │               │     Falls back to simulated data
              │  :9100/metrics│     when APIs are unavailable.
              └───────┬───────┘
                      │
                      │ publish
                      ▼
        ┌─────────────────────────────┐
        │     KAFKA CLUSTER           │
        │                             │
        │  ┌───────────────────────┐  │
        │  │  Topic: price-events  │  │     6 partitions
        │  │  Partitioned by       │  │     Keyed by symbol (BTC, ETH...)
        │  │  symbol               │  │     Sequential per-symbol ordering
        │  └───────────┬───────────┘  │
        │              │              │
        └──────────────┼──────────────┘
                       │
                       │ consume (price-aggregator-group)
                       ▼
              ┌───────────────┐
              │    PRICE      │     Computes: avg price, high/low,
              │  AGGREGATOR   │     24h change %, moving averages.
              │   SERVICE     │     Sliding window of 100 events.
              │               │     Horizontally scalable via
              │  :9101/metrics│     consumer group rebalancing.
              └───────┬───────┘
                      │
                      │ publish
                      ▼
        ┌─────────────────────────────┐
        │     KAFKA CLUSTER           │
        │                             │
        │  ┌───────────────────────┐  │
        │  │  Topic:               │  │     6 partitions
        │  │  aggregated-prices    │  │     Enriched price data
        │  └──┬────────┬────────┬──┘  │
        │     │        │        │     │
        └─────┼────────┼────────┼─────┘
              │        │        │
              │        │        │ consume (3 independent consumer groups)
              ▼        ▼        ▼
     ┌─────────┐ ┌─────────┐ ┌──────────────┐
     │  ALERT  │ │   DB    │ │  STREAMING   │
     │ SERVICE │ │ WRITER  │ │  API SERVICE │
     │         │ │         │ │              │
     │ Eval    │ │ Batch   │ │ Bridges Kafka│
     │ rules:  │ │ inserts │ │ to terminal  │
     │ BTC>65k │ │ into    │ │ clients via  │
     │ ETH<3k  │ │ Postgres│ │ Server-Sent  │
     │         │ │         │ │ Events (SSE) │
     │:9102    │ │:9103    │ │              │
     └────┬────┘ └─────────┘ │  :8080       │
          │                   └──────┬───────┘
          │ publish                   │
          ▼                           │ SSE stream
   ┌──────────────┐                   │
   │ Topic:       │                   ▼
   │ price-alerts │          ┌───────────────┐
   └──────────────┘          │   TERMINAL    │
                             │   CLI CLIENT  │
                             │               │
                             │ crypto-stream │
                             │  prices       │
                             │  watch        │
                             │  alerts       │
                             │  history      │
                             └───────────────┘
```

### Data Flow

```
Exchange API ──▶ Price Producer ──▶ Kafka [price-events]
                                          │
                                          ▼
                                   Price Aggregator ──▶ Kafka [aggregated-prices]
                                                              │
                                          ┌───────────────────┼──────────────────┐
                                          ▼                   ▼                  ▼
                                    Alert Service       DB Writer          Streaming API
                                          │                   │                  │
                                          ▼                   ▼                  ▼
                                   Kafka [price-alerts]   PostgreSQL      Terminal CLI (SSE)
```

### Kafka Topic Design

| Topic | Partitions | Key | Purpose |
|---|---|---|---|
| `price-events` | 6 | symbol | Raw price data from exchanges |
| `aggregated-prices` | 6 | symbol | Processed prices with analytics |
| `price-alerts` | 6 | symbol | Triggered alert notifications |

**Partition Strategy**: All topics are partitioned by cryptocurrency symbol. This guarantees that all events for a given asset (e.g., BTC) land on the same partition and are processed in order.

**Consumer Groups**: Each service runs in its own consumer group, enabling independent consumption and horizontal scaling:

| Consumer Group | Service | Scaling |
|---|---|---|
| `price-aggregator-group` | Price Aggregator | Add instances to split partitions |
| `alert-service-group` | Alert Service | Add instances for parallel rule evaluation |
| `db-writer-group` | DB Writer | Add instances for write throughput |
| `streaming-api-group` | Streaming API | Add instances behind a load balancer |

---

## Project Structure

```
termino/
├── cli/
│   └── crypto-stream/              # Terminal CLI application
│       ├── main.go                 # CLI entry point & subcommand routing
│       └── ui/
│           ├── prices.go           # Live multi-asset price table (Bubble Tea)
│           ├── watch.go            # Single-asset detail view with sparkline
│           ├── alerts.go           # Real-time alert stream
│           └── history.go          # Historical price viewer
├── services/
│   ├── price-producer/             # Fetches prices from CoinGecko, publishes to Kafka
│   ├── price-aggregator/           # Consumes raw prices, computes aggregates, republishes
│   ├── alert-service/              # Evaluates price rules, triggers alerts
│   ├── db-writer/                  # Batch-writes aggregated prices to PostgreSQL
│   └── streaming-api/              # SSE bridge from Kafka to terminal clients
├── pkg/
│   ├── kafka/                      # Kafka producer, consumer, topic management
│   ├── config/                     # Environment-based configuration loader
│   ├── logger/                     # Zap structured JSON logging
│   └── models/                     # Shared domain types (PriceEvent, AggregatedPrice, PriceAlert)
├── internal/
│   └── domain/
│       ├── aggregator.go           # Price aggregation engine (sliding window)
│       └── alertengine.go          # Alert rule evaluation engine
├── deployments/
│   ├── docker-compose.yml          # Full stack deployment
│   ├── prometheus/
│   │   └── prometheus.yml          # Prometheus scrape targets
│   └── grafana/
│       └── provisioning/           # Auto-provisioned datasource + dashboard
├── .env                            # Local environment variables (git-ignored)
├── .env.example                    # Environment template
├── Dockerfile                      # Multi-stage build for all services
├── Makefile                        # Build, run, deploy commands
├── go.mod
└── go.sum
```

---

## Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.24+ |
| Streaming | Apache Kafka (Confluent 7.6) |
| Database | PostgreSQL 16 |
| Cache | Redis 7 |
| Terminal UI | Bubble Tea + Lipgloss |
| Kafka Client | segmentio/kafka-go |
| Logging | Zap (structured JSON) |
| Metrics | Prometheus + Grafana |
| Containers | Docker + Docker Compose |

---

## Prerequisites

- **Go** 1.24+ ([install](https://go.dev/dl/))
- **Docker** and **Docker Compose** ([install](https://docs.docker.com/get-docker/))
- **Make** (usually pre-installed on macOS/Linux)

---

## Getting Started

### Step 1: Clone and configure

```bash
git clone https://github.com/uwezukwechibuzor/termino.git
cd termino
cp .env.example .env
```

Review `.env` and adjust any values if needed. The defaults work out of the box for local development.

### Step 2: Start all services

**Option A: Run everything with Docker (recommended)**

A single command builds and starts all infrastructure (Kafka, Zookeeper, PostgreSQL, Redis, Prometheus, Grafana) and all application services in Docker containers:

```bash
make up
```

Check that all services are running:

```bash
make status
```

View logs:

```bash
make logs
```

To stop everything:

```bash
make down
```

**Option B: Run services locally (for development)**

Start infrastructure first:

```bash
make infra
```

Wait ~15-20 seconds for Kafka to become healthy, then run all services in the background:

```bash
make run-all
```

Or run each service in a separate terminal for individual log visibility:

```bash
make run-producer      # Terminal 1 - Fetches prices, publishes to Kafka
make run-aggregator    # Terminal 2 - Computes averages, change %
make run-alerts        # Terminal 3 - Evaluates price rules
make run-dbwriter      # Terminal 4 - Persists to PostgreSQL
make run-streaming     # Terminal 5 - SSE server for CLI clients
```

### Step 3: Build and use the CLI

```bash
make build-cli
```

This creates `bin/crypto-stream`. Now use it:

```bash
# Stream live prices for specific symbols
./bin/crypto-stream prices BTC ETH SOL

# Stream all default symbols
./bin/crypto-stream prices

# Watch a single asset with detailed view and sparkline
./bin/crypto-stream watch BTC

# Monitor live alerts
./bin/crypto-stream alerts

# View latest price snapshot
./bin/crypto-stream history BTC
```

To install the CLI globally:

```bash
make install-cli
crypto-stream prices BTC ETH SOL
```

### Step 4: Access monitoring dashboards

| Service | URL | Credentials |
|---|---|---|
| Prometheus | http://localhost:9090 | - |
| Grafana | http://localhost:3000 | admin / admin |
| Streaming API | http://localhost:8080/prices | - |
| SSE Stream | http://localhost:8080/stream?symbols=BTC,ETH | - |

---

## CLI Commands

### `crypto-stream prices [SYMBOLS...]`

Streams a live-updating price table with color-coded changes.

```
 CRYPTO PRICE STREAM

 SYMBOL   PRICE          CHANGE %    VOLUME         UPDATED
 BTC      $64,012.22     +0.35%      1.24B          14:32:05
 ETH      $3,211.11      -0.12%      892.50M        14:32:05
 SOL      $145.67        +1.02%      345.20M        14:32:05

 ● CONNECTED | Press q to quit
```

### `crypto-stream watch SYMBOL`

Detailed single-asset view with price sparkline history.

```
 WATCHING BTC

 $64,012.22 ▲

 ╭──────────────────────────────────────────────╮
 │ Average Price:  $63,998.50                   │
 │ 24h High:       $64,150.00                   │
 │ 24h Low:        $63,800.00                   │
 │ Change:         +0.35%                       │
 │ Volume:         1.24B                        │
 │ Exchanges:      1                            │
 │ Updated:        14:32:05                     │
 ╰──────────────────────────────────────────────╯

 Price History (last 60 updates):
 ▃▄▅▆▅▄▃▄▅▆▇▆▅▄▅▆▇█▇▆▅▆▇▆▅▄▃▄▅▆▅

 ● LIVE  |  Press q to quit
```

### `crypto-stream alerts`

Streams triggered price alerts in real time.

### `crypto-stream history SYMBOL`

Fetches the latest aggregated price snapshot. Press `r` to refresh.

---

## Environment Variables

All services read configuration from environment variables. The `.env` file is auto-loaded by the Makefile.

| Variable | Default | Description |
|---|---|---|
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka broker addresses |
| `KAFKA_TOPIC_PRICES` | `price-events` | Raw price events topic |
| `KAFKA_TOPIC_AGG` | `aggregated-prices` | Aggregated prices topic |
| `KAFKA_TOPIC_ALERTS` | `price-alerts` | Price alerts topic |
| `POSTGRES_DSN` | `postgres://crypto:crypto@localhost:5432/cryptodb?sslmode=disable` | PostgreSQL connection string |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `HTTP_PORT` | `8080` | Streaming API HTTP port |
| `PROMETHEUS_PORT` | `9090` | Prometheus metrics port |
| `POLL_INTERVAL` | `5` | Price polling interval in seconds |
| `SYMBOLS` | `BTC,ETH,SOL,ADA,DOT,AVAX,MATIC,LINK` | Symbols to track |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `STREAM_URL` | `http://localhost:8080` | Streaming API URL (for CLI) |

---

## Observability

### Metrics (Prometheus)

Each service exposes a `/metrics` endpoint:

| Metric | Type | Service | Description |
|---|---|---|---|
| `prices_produced_total` | Counter | Price Producer | Total price events published |
| `api_latency_seconds` | Histogram | Price Producer | Exchange API call latency |
| `api_errors_total` | Counter | Price Producer | API call failures |
| `prices_consumed_total` | Counter | Aggregator | Price events consumed |
| `aggregations_published_total` | Counter | Aggregator | Aggregated prices published |
| `alerts_triggered_total` | Counter | Alert Service | Alerts triggered by symbol |
| `db_rows_inserted_total` | Counter | DB Writer | Rows inserted to PostgreSQL |
| `db_batches_written_total` | Counter | DB Writer | Batch write operations |
| `streaming_active_clients` | Gauge | Streaming API | Connected SSE clients |
| `messages_streamed_total` | Counter | Streaming API | Messages sent to clients |

### Grafana Dashboard

A pre-provisioned dashboard is available at http://localhost:3000 with panels for:
- Price production/consumption rates
- API latency (p95)
- Active streaming clients
- Alert trigger counts
- Database write throughput
- API error rates

### Structured Logging

All services emit structured JSON logs via Zap:

```json
{"level":"info","ts":"2026-03-10T14:32:05.123Z","service":"price-producer","msg":"published price","symbol":"BTC","price":64012.22,"exchange":"coingecko"}
```

---

## Scaling

### Horizontal Scaling via Kafka Consumer Groups

Each service uses an independent Kafka consumer group. To scale a service:

1. **Start additional instances** of the same service
2. Kafka automatically **rebalances partitions** across group members
3. With 6 partitions per topic, each service can scale to **up to 6 instances**

```
                        ┌──────────────────┐
                        │ aggregated-prices │
                        │   (6 partitions)  │
                        └─┬──┬──┬──┬──┬──┬─┘
                          │  │  │  │  │  │
    ┌─────────────────────┘  │  │  │  │  └─────────────────────┐
    ▼        ▼               ▼  ▼               ▼              ▼
┌────────┐ ┌────────┐  ┌────────┐ ┌────────┐ ┌────────┐  ┌────────┐
│ P0, P1 │ │ P2, P3 │  │ P4, P5 │ │ P0, P1 │ │ P2, P3 │  │ P4, P5 │
│        │ │        │  │        │ │        │ │        │  │        │
│DB Writer│ │DB Writer│ │DB Writer│ │Alert   │ │Alert   │  │Alert   │
│ Inst 1 │ │ Inst 2 │  │ Inst 3 │ │ Inst 1 │ │ Inst 2 │  │ Inst 3 │
└────────┘ └────────┘  └────────┘ └────────┘ └────────┘  └────────┘

  db-writer-group (3 instances)     alert-service-group (3 instances)
```

### Multiple Producer Instances

Run multiple price producer instances pointing at different exchanges for broader market coverage. The Kafka `Hash` partitioner ensures consistent symbol-to-partition mapping.

---

## Fault Tolerance

| Mechanism | Implementation |
|---|---|
| **API Fallback** | Price producer falls back to simulated prices when CoinGecko is unavailable |
| **Retry Logic** | Kafka producer retries up to 3 times with backoff |
| **Consumer Offsets** | Committed after successful processing only |
| **Batch Writes** | DB writer uses transactions; failed batches are retried |
| **Idempotent Inserts** | `ON CONFLICT DO NOTHING` prevents duplicate price rows |
| **Dead Letter Queue** | Hook point in consumer for failed message routing |
| **Graceful Shutdown** | All services handle SIGINT/SIGTERM with cleanup |
| **Health Checks** | Each service exposes `/health` for Docker/k8s probes |

---

## Useful Commands

```bash
make build-all      # Build all services + CLI
make infra          # Start infrastructure only
make up             # Start everything via Docker
make down           # Stop all Docker services
make run-all        # Run all services locally in background
make stop-all       # Stop all locally running services
make status         # Show Docker service status
make logs           # Tail all Docker logs
make test           # Run Go tests
make clean          # Remove binaries + Docker volumes
```

---

## License

MIT
