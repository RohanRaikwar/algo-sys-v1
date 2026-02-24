# Trading System v1

A high-performance, real-time trading system built in Go for the Indian stock market via Angel One SmartAPI.

## Architecture

```
WS Ticks → Aggregator → 1s OHLC Candles → Fan-out
                                             ├── Redis (realtime)
                                             ├── SQLite (persistence)
                                             ├── Strategy Engine (signals)
                                             └── API Server (REST/WS)
```

## Project Structure

```
trading-systemv1/
├── cmd/                          # Application entry points
│   ├── mdengine/                 #   Market data engine (live pipeline)
│   ├── api/                      #   REST/gRPC API server
│   └── backtest/                 #   Backtesting runner
│
├── config/                       # Centralized configuration
│
├── internal/                     # Private application code
│   ├── marketdata/               #   Market data pipeline
│   │   ├── ws/                   #     WebSocket ingest (Angel One)
│   │   ├── agg/                  #     1-second OHLC aggregator
│   │   └── bus/                  #     Fan-out broadcaster
│   ├── model/                    #   Domain models (Tick, Candle, Order, etc.)
│   ├── store/                    #   Persistence layer
│   │   ├── redis/                #     Redis writer (realtime)
│   │   └── sqlite/               #     SQLite writer (durable)
│   ├── strategy/                 #   Strategy engine & signal generation
│   ├── execution/                #   Order execution via broker API
│   ├── portfolio/                #   Position tracking & risk management
│   ├── indicator/                #   Technical indicators (SMA, EMA, etc.)
│   ├── api/                      #   HTTP/gRPC API handlers
│   ├── notification/             #   Alert delivery (Telegram, Discord, etc.)
│   └── metrics/                  #   Prometheus metrics & health checks
│
├── pkg/                          # Public/reusable packages
│   └── smartconnect/             #   Angel One SmartAPI SDK (REST + WebSocket)
│
├── docs/                         # Documentation
├── scripts/                      # Ops & dev scripts
└── data/                         # Runtime data (SQLite DB, logs)
```

## Quick Start

```bash
# 1. Copy env file and fill in credentials
cp config.env.example .env

# 2. Run the market data engine
set -a && source .env && set +a && go run ./cmd/mdengine/
```

## Configuration

All configuration is via environment variables (see `config.env.example`):

| Variable | Description | Default |
|---|---|---|
| `ANGEL_API_KEY` | Angel One API key | required |
| `ANGEL_CLIENT_CODE` | Angel One client code | required |
| `ANGEL_PASSWORD` | Angel One password | required |
| `ANGEL_TOTP_SECRET` | TOTP secret for auto-generation | required |
| `REDIS_ADDR` | Redis address | `localhost:6379` |
| `SQLITE_PATH` | SQLite database path | `data/candles.db` |
| `METRICS_ADDR` | Prometheus metrics server addr | `:9090` |
| `SUBSCRIBE_TOKENS` | Tokens to subscribe (format: `exchange_type:token`) | `1:99926000` (NIFTY 50) |

## Development

```bash
# Run tests
go test ./...

# Build
go build ./...

# Run with dev script
./scripts/run_dev.sh
```
