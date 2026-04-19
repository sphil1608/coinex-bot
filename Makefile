BINARY        := coinex-bot
BACKTEST_BIN  := coinex-backtest
OPTIMIZE_BIN  := coinex-optimize
CMD_BOT       := ./cmd/bot
CMD_BACKTEST  := ./cmd/backtest
CMD_OPTIMIZE  := ./cmd/optimize
CONFIG        := configs/config.yaml
BIN_DIR       := bin
RESULTS_DIR   := results
LDFLAGS       := -ldflags="-s -w"

.PHONY: all build build-all run paper live backtest backtest-synthetic \
        backtest-ichimoku backtest-all optimize optimize-rsi optimize-ema \
        optimize-breakout test test-race test-cover bench \
        docker-build docker-up docker-up-monitoring docker-down docker-logs \
        tidy fmt vet lint deps clean help

all: build-all

build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) $(CMD_BOT)
	@echo "✓ built $(BIN_DIR)/$(BINARY)"

build-all:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY)       $(CMD_BOT)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BACKTEST_BIN) $(CMD_BACKTEST)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(OPTIMIZE_BIN) $(CMD_OPTIMIZE)
	@echo "✓ all binaries built"

run: build
	./$(BIN_DIR)/$(BINARY) --config $(CONFIG)

paper: build
	@echo "⚠️  Paper trading mode"
	./$(BIN_DIR)/$(BINARY) --config $(CONFIG)

live: build
	@echo "🔴  LIVE TRADING — double-check your config!"
	@read -p "Press ENTER to continue or Ctrl+C to abort..." _
	./$(BIN_DIR)/$(BINARY) --config $(CONFIG)

backtest: build
	@mkdir -p $(RESULTS_DIR)
	./$(BIN_DIR)/$(BACKTEST_BIN) --config $(CONFIG) --out $(RESULTS_DIR)

backtest-synthetic: build
	@mkdir -p $(RESULTS_DIR)
	./$(BIN_DIR)/$(BACKTEST_BIN) --synthetic --out $(RESULTS_DIR)
	@echo "✓ results in $(RESULTS_DIR)/"

backtest-ichimoku: build
	./$(BIN_DIR)/$(BACKTEST_BIN) --synthetic --strategy ichimoku

backtest-all: build
	@mkdir -p $(RESULTS_DIR)
	./$(BIN_DIR)/$(BACKTEST_BIN) --synthetic --strategy all --out $(RESULTS_DIR)

optimize: build
	./$(BIN_DIR)/$(OPTIMIZE_BIN) --strategy rsi --metric sharpe

optimize-rsi: build
	./$(BIN_DIR)/$(OPTIMIZE_BIN) --strategy rsi --metric sharpe

optimize-ema: build
	./$(BIN_DIR)/$(OPTIMIZE_BIN) --strategy ema --metric sharpe

optimize-breakout: build
	./$(BIN_DIR)/$(OPTIMIZE_BIN) --strategy breakout --metric profit_factor

test:
	go test ./... -v -count=1

test-race:
	go test ./... -v -race -count=1

test-cover:
	@mkdir -p $(RESULTS_DIR)
	go test ./... -coverprofile=$(RESULTS_DIR)/coverage.out
	go tool cover -html=$(RESULTS_DIR)/coverage.out -o $(RESULTS_DIR)/coverage.html
	@echo "✓ coverage: $(RESULTS_DIR)/coverage.html"

bench:
	go test ./... -bench=. -benchmem -run='^$$'

docker-build:
	docker build -t coinex-bot:latest .

docker-up:
	docker compose up -d
	@echo "✓ http://localhost:8080"

docker-up-monitoring:
	docker compose --profile monitoring up -d
	@echo "✓ Grafana: http://localhost:3000"

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f bot

tidy:
	go mod tidy

fmt:
	gofmt -w -s .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

deps:
	go mod download

clean:
	rm -rf $(BIN_DIR)/ $(RESULTS_DIR)/

help:
	@printf "\n  %-28s %s\n" "Target" "Description"
	@printf "  %-28s %s\n" "──────────────────────────" "──────────────────────────────────────"
	@printf "  %-28s %s\n" "build"            "Compile bot binary"
	@printf "  %-28s %s\n" "build-all"        "Compile bot + backtest + optimize"
	@printf "  %-28s %s\n" "run / paper"      "Start bot in paper mode"
	@printf "  %-28s %s\n" "live"             "Start bot in live mode (prompts)"
	@printf "  %-28s %s\n" "backtest-synthetic" "All strategies on synthetic data"
	@printf "  %-28s %s\n" "backtest-ichimoku" "Backtest Ichimoku only"
	@printf "  %-28s %s\n" "optimize"         "Walk-forward optimise RSI params"
	@printf "  %-28s %s\n" "optimize-ema"     "Walk-forward optimise EMA params"
	@printf "  %-28s %s\n" "test"             "Run all unit tests"
	@printf "  %-28s %s\n" "test-race"        "Tests with race detector"
	@printf "  %-28s %s\n" "test-cover"       "HTML coverage report"
	@printf "  %-28s %s\n" "docker-build"     "Build Docker image"
	@printf "  %-28s %s\n" "docker-up"        "Start with Docker Compose"
	@printf "  %-28s %s\n" "docker-up-monitoring" "Start with Grafana + Prometheus"
	@printf "  %-28s %s\n" "clean"            "Remove binaries and results"
	@echo ""
