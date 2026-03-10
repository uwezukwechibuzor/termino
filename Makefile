.PHONY: build build-cli run-producer run-aggregator run-alerts run-dbwriter run-streaming run-all infra up down clean test lint env-check

# Load .env file if present
ifneq (,$(wildcard ./.env))
	include .env
	export
endif

# Build all services
build:
	@echo "Building all services..."
	@mkdir -p bin
	go build -o bin/price-producer ./services/price-producer/
	go build -o bin/price-aggregator ./services/price-aggregator/
	go build -o bin/alert-service ./services/alert-service/
	go build -o bin/db-writer ./services/db-writer/
	go build -o bin/streaming-api ./services/streaming-api/
	@echo "All services built in bin/"

# Build CLI
build-cli:
	@echo "Building CLI..."
	@mkdir -p bin
	go build -o bin/crypto-stream ./cli/crypto-stream/
	@echo "CLI built: bin/crypto-stream"

# Build everything
build-all: build build-cli

# Run individual services locally (loads .env automatically)
run-producer:
	go run ./services/price-producer/

run-aggregator:
	go run ./services/price-aggregator/

run-alerts:
	go run ./services/alert-service/

run-dbwriter:
	go run ./services/db-writer/

run-streaming:
	go run ./services/streaming-api/

# Run all services locally (each in background)
run-all:
	@echo "Starting all services locally..."
	@echo "Make sure infrastructure is running: make infra"
	@echo ""
	go run ./services/price-producer/ &
	go run ./services/price-aggregator/ &
	go run ./services/alert-service/ &
	go run ./services/db-writer/ &
	go run ./services/streaming-api/ &
	@echo ""
	@echo "All services started. Use 'make stop-all' to stop them."

# Stop all locally running Go services
stop-all:
	@echo "Stopping all services..."
	@-pkill -f "go run ./services/" 2>/dev/null || true
	@-pkill -f "bin/price-producer" 2>/dev/null || true
	@-pkill -f "bin/price-aggregator" 2>/dev/null || true
	@-pkill -f "bin/alert-service" 2>/dev/null || true
	@-pkill -f "bin/db-writer" 2>/dev/null || true
	@-pkill -f "bin/streaming-api" 2>/dev/null || true
	@echo "All services stopped."

# Start infrastructure only (Kafka, Postgres, Redis, Prometheus, Grafana)
infra:
	docker compose -f deployments/docker-compose.yml up -d zookeeper kafka postgres redis prometheus grafana
	@echo ""
	@echo "Infrastructure started. Waiting for Kafka to be healthy..."
	@echo "Run 'docker compose -f deployments/docker-compose.yml logs -f kafka' to monitor."

# Start everything via Docker
up:
	docker compose -f deployments/docker-compose.yml up -d --build
	@echo ""
	@echo "All services started via Docker."
	@echo "  Streaming API: http://localhost:8080"
	@echo "  Prometheus:    http://localhost:9090"
	@echo "  Grafana:       http://localhost:3000 (admin/admin)"

# Stop everything
down:
	docker compose -f deployments/docker-compose.yml down

# Clean build artifacts and volumes
clean:
	rm -rf bin/
	docker compose -f deployments/docker-compose.yml down -v

# Run tests
test:
	go test ./... -v

# Install CLI globally
install-cli: build-cli
	cp bin/crypto-stream $(GOPATH)/bin/
	@echo "Installed crypto-stream to $(GOPATH)/bin/"

# Show status of Docker services
status:
	docker compose -f deployments/docker-compose.yml ps

# View logs
logs:
	docker compose -f deployments/docker-compose.yml logs -f

# Check env file exists
env-check:
	@if [ ! -f .env ]; then \
		echo "No .env file found. Copying from .env.example..."; \
		cp .env.example .env; \
		echo "Created .env - review and adjust values as needed."; \
	else \
		echo ".env file exists."; \
	fi
