.PHONY: build build-cli run-producer run-aggregator run-alerts run-dbwriter run-streaming infra up down clean

# Build all services
build:
	go build -o bin/price-producer ./services/price-producer/
	go build -o bin/price-aggregator ./services/price-aggregator/
	go build -o bin/alert-service ./services/alert-service/
	go build -o bin/db-writer ./services/db-writer/
	go build -o bin/streaming-api ./services/streaming-api/

# Build CLI
build-cli:
	go build -o bin/crypto-stream ./cli/crypto-stream/

# Run individual services locally
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

# Start infrastructure only
infra:
	docker compose -f deployments/docker-compose.yml up -d zookeeper kafka postgres redis prometheus grafana

# Start everything
up:
	docker compose -f deployments/docker-compose.yml up -d --build

# Stop everything
down:
	docker compose -f deployments/docker-compose.yml down

# Clean build artifacts
clean:
	rm -rf bin/
	docker compose -f deployments/docker-compose.yml down -v

# Install CLI globally
install-cli: build-cli
	cp bin/crypto-stream $(GOPATH)/bin/
