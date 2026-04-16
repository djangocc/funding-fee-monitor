# Spread Arbitrage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a cross-exchange perpetual futures spread convergence arbitrage system with a Go backend and React frontend.

**Architecture:** Single Go process using goroutines for concurrent WebSocket connections, a strategy engine, and an HTTP/WS API server. React frontend communicates via REST + WebSocket. All state is in-memory. Exchange credentials loaded from `.env` via `godotenv`.

**Tech Stack:** Go 1.26, React (TypeScript), gorilla/websocket, godotenv, gin (HTTP router)

**Spec:** `docs/superpowers/specs/2026-04-16-spread-arbitrage-design.md`

---

## File Map

```
spread-arbitrage/
├── .env.example                           # API key template
├── .gitignore                             # .env, node_modules, dist
├── go.mod
├── go.sum
├── main.go                                # Entry point: loads config, starts all components
├── config.go                              # Loads .env, exposes exchange credentials
├── internal/
│   ├── model/
│   │   └── types.go                       # Shared types: BookTicker, Task, Order, Position, etc.
│   ├── exchange/
│   │   ├── client.go                      # ExchangeClient interface definition
│   │   ├── binance.go                     # Binance futures: WS bookTicker + REST order/position/trades
│   │   ├── aster.go                       # Aster futures (Binance-compatible API)
│   │   └── okx.go                         # OKX futures: WS tickers + REST order/position/trades
│   ├── engine/
│   │   ├── task.go                        # Task struct, TaskManager (CRUD, in-memory store)
│   │   ├── spread.go                      # Spread calculation, confirm counter logic
│   │   └── engine.go                      # Main engine loop: receives ticks, evaluates tasks, executes trades
│   ├── api/
│   │   ├── server.go                      # Gin server setup, static file serving
│   │   ├── routes.go                      # REST route handlers for /api/tasks/*
│   │   └── ws.go                          # WebSocket hub: broadcast spread updates + events to frontend
│   └── wsmanager/
│       └── manager.go                     # Manages exchange WS connections, latency checks, reconnection
├── internal/engine/engine_test.go         # Engine unit tests
├── internal/engine/spread_test.go         # Spread calculation + confirm counter tests
├── internal/engine/task_test.go           # TaskManager tests
├── web/                                   # React frontend
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/
│       ├── main.tsx                       # React entry
│       ├── App.tsx                        # Main layout
│       ├── api.ts                         # REST API client
│       ├── hooks/
│       │   └── useWebSocket.ts            # WS connection + auto-reconnect
│       └── components/
│           ├── TaskList.tsx                # Task cards with controls
│           ├── TaskDetail.tsx              # Positions + trades tables
│           └── TaskForm.tsx               # Create/edit task form
```

---

## Task 1: Project Scaffolding & Config

**Files:**
- Create: `spread-arbitrage/go.mod`
- Create: `spread-arbitrage/main.go`
- Create: `spread-arbitrage/config.go`
- Create: `spread-arbitrage/.env.example`
- Create: `spread-arbitrage/.gitignore`

- [ ] **Step 1: Initialize Go module**

```bash
cd spread-arbitrage
go mod init spread-arbitrage
```

- [ ] **Step 2: Create `.env.example`**

```env
# Binance Futures
BINANCE_API_KEY=
BINANCE_API_SECRET=

# Aster Futures
ASTER_API_KEY=
ASTER_API_SECRET=

# OKX Futures
OKX_API_KEY=
OKX_API_SECRET=
OKX_PASSPHRASE=

# Server
PORT=8080
```

- [ ] **Step 3: Create `.gitignore`**

```
.env
node_modules/
dist/
web/dist/
```

- [ ] **Step 4: Create `config.go`**

Loads `.env` using `godotenv`. Exposes a `Config` struct with exchange credentials and server port. Each exchange has `APIKey`, `APISecret`, and optionally `Passphrase`. Reads from environment variables.

```go
package main

import (
    "log"
    "os"

    "github.com/joho/godotenv"
)

type ExchangeConfig struct {
    APIKey    string
    APISecret string
    Passphrase string // OKX only
}

type Config struct {
    Port     string
    Binance  ExchangeConfig
    Aster    ExchangeConfig
    OKX      ExchangeConfig
}

func LoadConfig() Config {
    if err := godotenv.Load(); err != nil {
        log.Println("No .env file found, using environment variables")
    }
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    return Config{
        Port: port,
        Binance: ExchangeConfig{
            APIKey:    os.Getenv("BINANCE_API_KEY"),
            APISecret: os.Getenv("BINANCE_API_SECRET"),
        },
        Aster: ExchangeConfig{
            APIKey:    os.Getenv("ASTER_API_KEY"),
            APISecret: os.Getenv("ASTER_API_SECRET"),
        },
        OKX: ExchangeConfig{
            APIKey:    os.Getenv("OKX_API_KEY"),
            APISecret: os.Getenv("OKX_API_SECRET"),
            Passphrase: os.Getenv("OKX_PASSPHRASE"),
        },
    }
}
```

- [ ] **Step 5: Create minimal `main.go`**

```go
package main

import (
    "fmt"
    "log"
)

func main() {
    cfg := LoadConfig()
    log.Printf("Starting spread-arbitrage on port %s", cfg.Port)
    fmt.Println("Server starting...")
}
```

- [ ] **Step 6: Install dependencies and verify build**

```bash
cd spread-arbitrage
go get github.com/joho/godotenv
go get github.com/google/uuid
go get github.com/gorilla/websocket
go get github.com/gin-gonic/gin
go get github.com/gin-contrib/cors
go build ./...
```

Expected: compiles with no errors.

- [ ] **Step 7: Commit**

```bash
git add spread-arbitrage/
git commit -m "feat(spread-arb): project scaffolding with config loading"
```

---

## Task 2: Shared Types

**Files:**
- Create: `spread-arbitrage/internal/model/types.go`

- [ ] **Step 1: Create `types.go`**

All shared data structures used across packages. This is the single source of truth for data shapes.

```go
package model

import "time"

// BookTicker represents best bid/ask from an exchange
type BookTicker struct {
    Exchange  string
    Symbol    string
    Bid       float64
    Ask       float64
    Timestamp time.Time // exchange-side timestamp
    ReceivedAt time.Time // local receive time
}

// Direction of the spread trade
type Direction string

const (
    LongSpread  Direction = "long_spread"
    ShortSpread Direction = "short_spread"
)

// TaskStatus
type TaskStatus string

const (
    StatusRunning TaskStatus = "running"
    StatusStopped TaskStatus = "stopped"
)

// Task represents an arbitrage task configuration + runtime state
type Task struct {
    ID               string     `json:"id"`
    Symbol           string     `json:"symbol"`
    ExchangeA        string     `json:"exchange_a"`
    ExchangeB        string     `json:"exchange_b"`
    Direction        Direction  `json:"direction"`
    Status           TaskStatus `json:"status"`
    OpenThreshold    float64    `json:"open_threshold"`
    CloseThreshold   float64    `json:"close_threshold"`
    ConfirmCount     int        `json:"confirm_count"`
    QuantityPerOrder float64    `json:"quantity_per_order"`
    MaxPositionQty   float64    `json:"max_position_qty"`
    DataMaxLatencyMs int64      `json:"data_max_latency_ms"`
}

// TaskCreateRequest is the JSON body for POST /api/tasks
type TaskCreateRequest struct {
    Symbol           string    `json:"symbol" binding:"required"`
    ExchangeA        string    `json:"exchange_a" binding:"required"`
    ExchangeB        string    `json:"exchange_b" binding:"required"`
    Direction        Direction `json:"direction" binding:"required"`
    OpenThreshold    float64   `json:"open_threshold" binding:"required"`
    CloseThreshold   float64   `json:"close_threshold" binding:"required"`
    ConfirmCount     int       `json:"confirm_count" binding:"required"`
    QuantityPerOrder float64   `json:"quantity_per_order" binding:"required"`
    MaxPositionQty   float64   `json:"max_position_qty" binding:"required"`
    DataMaxLatencyMs int64     `json:"data_max_latency_ms" binding:"required"`
}

// Order represents a submitted order result
type Order struct {
    Exchange  string  `json:"exchange"`
    Symbol    string  `json:"symbol"`
    Side      string  `json:"side"` // "BUY" or "SELL"
    Quantity  float64 `json:"quantity"`
    Price     float64 `json:"price"` // fill price
    OrderID   string  `json:"order_id"`
    Timestamp time.Time `json:"timestamp"`
}

// Position from exchange
type Position struct {
    Exchange      string  `json:"exchange"`
    Symbol        string  `json:"symbol"`
    Side          string  `json:"side"` // "LONG", "SHORT", or ""
    Size          float64 `json:"size"`
    EntryPrice    float64 `json:"entry_price"`
    UnrealizedPnL float64 `json:"unrealized_pnl"`
}

// Trade record from exchange
type Trade struct {
    Exchange  string    `json:"exchange"`
    Symbol    string    `json:"symbol"`
    Side      string    `json:"side"`
    Quantity  float64   `json:"quantity"`
    Price     float64   `json:"price"`
    Fee       float64   `json:"fee"`
    Timestamp time.Time `json:"timestamp"`
    OrderID   string    `json:"order_id"`
}

// WSEvent is pushed to frontend via WebSocket
type WSEvent struct {
    Type   string      `json:"type"` // "spread_update", "task_status", "trade_executed", "error"
    TaskID string      `json:"task_id"`
    Data   interface{} `json:"data"`
}

// SpreadUpdate is the Data payload for "spread_update" events
type SpreadUpdate struct {
    Symbol       string  `json:"symbol"`
    ExchangeA    string  `json:"exchange_a"`
    ExchangeB    string  `json:"exchange_b"`
    BidA         float64 `json:"bid_a"`
    AskA         float64 `json:"ask_a"`
    BidB         float64 `json:"bid_b"`
    AskB         float64 `json:"ask_b"`
    Spread       float64 `json:"spread"`       // current spread used for signal evaluation
    OpenCounter  int     `json:"open_counter"`  // current consecutive open confirm count
    CloseCounter int     `json:"close_counter"` // current consecutive close confirm count
}
```

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/internal/model/
git commit -m "feat(spread-arb): add shared model types"
```

---

## Task 3: Exchange Client Interface & Binance Implementation

**Files:**
- Create: `spread-arbitrage/internal/exchange/client.go`
- Create: `spread-arbitrage/internal/exchange/binance.go`

- [ ] **Step 1: Create `client.go` — interface definition**

```go
package exchange

import (
    "context"
    "spread-arbitrage/internal/model"
)

// Client is the unified exchange interface.
// All exchange implementations must satisfy this.
type Client interface {
    // Name returns the exchange identifier ("binance", "aster", "okx")
    Name() string

    // SubscribeBookTicker opens a WS connection and streams BookTicker updates.
    // The returned channel is closed when the context is cancelled or connection drops.
    // The implementation must handle reconnection internally when possible.
    SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error)

    // PlaceMarketOrder places a market order. Returns the filled order details.
    PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64) (*model.Order, error)

    // GetPosition returns the current position for a symbol.
    GetPosition(ctx context.Context, symbol string) (*model.Position, error)

    // GetTrades returns recent trades for a symbol.
    GetTrades(ctx context.Context, symbol string) ([]model.Trade, error)

    // Close cleans up connections.
    Close() error
}
```

- [ ] **Step 2: Create `binance.go` — Binance futures client**

Implements `Client` interface for Binance futures API:
- **WS:** `wss://fstream.binance.com/ws/{symbol_lower}@bookTicker`
- **REST base:** `https://fapi.binance.com`
- **Signing:** HMAC-SHA256, query string signature
- **Endpoints:**
  - `POST /fapi/v1/order` (place order)
  - `GET /fapi/v2/positionRisk` (get position)
  - `GET /fapi/v1/userTrades` (get trades)

Key implementation details:
- `SubscribeBookTicker`: connects to WS, parses `{"s":"RAVEUSDT","b":"0.123","a":"0.124","T":1234567890123}`, converts to `model.BookTicker`. On disconnect, log and attempt reconnect with 3s backoff up to 10 retries, then close the channel.
- `PlaceMarketOrder`: signs request, sends POST, parses response including `avgPrice` and `executedQty`.
- `GetPosition`: signs request, filters response array by symbol.
- `GetTrades`: signs request, returns last 50 trades for the symbol.
- HMAC signing: `timestamp` param + `signature` = HMAC-SHA256 of query string.
- Header: `X-MBX-APIKEY`.

```go
package exchange

// Full implementation of BinanceClient struct implementing Client interface.
// Constructor: NewBinanceClient(apiKey, apiSecret string) *BinanceClient
// Uses net/http for REST, gorilla/websocket for WS.
```

- [ ] **Step 3: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add spread-arbitrage/internal/exchange/
git commit -m "feat(spread-arb): exchange client interface + Binance implementation"
```

---

## Task 4: Aster Exchange Client

**Files:**
- Create: `spread-arbitrage/internal/exchange/aster.go`

- [ ] **Step 1: Create `aster.go`**

Aster uses the same API format as Binance, with different base URLs:
- **WS:** `wss://fstream.asterdex.com/ws/{symbol_lower}@bookTicker`
- **REST base:** `https://fapi.asterdex.com`
- **Note:** Aster may use self-signed SSL certificates, so the HTTP client and WS dialer must skip TLS verification.

Implementation: Reuse the Binance client struct internally. Create `NewAsterClient(apiKey, apiSecret string)` that constructs a `BinanceClient` with Aster URLs and `InsecureSkipVerify: true` on the TLS config.

```go
package exchange

// NewAsterClient returns a Client backed by Aster's Binance-compatible API.
// Internally creates a BinanceClient with Aster endpoints and TLS skip.
func NewAsterClient(apiKey, apiSecret string) *BinanceClient {
    // ... set baseURL, wsURL, configure TLS skip
}
```

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/internal/exchange/aster.go
git commit -m "feat(spread-arb): Aster exchange client (Binance-compatible)"
```

---

## Task 5: OKX Exchange Client

**Files:**
- Create: `spread-arbitrage/internal/exchange/okx.go`

- [ ] **Step 1: Create `okx.go`**

OKX has a different API format:
- **Symbol mapping:** `RAVEUSDT` → `RAVE-USDT-SWAP`
- **WS:** `wss://ws.okx.com:8443/ws/v5/public`, subscribe with `{"op":"subscribe","args":[{"channel":"tickers","instId":"RAVE-USDT-SWAP"}]}`
- **WS message format:** `{"data":[{"instId":"...","bidPx":"0.123","askPx":"0.124","ts":"1234567890123"}]}`
- **REST base:** `https://www.okx.com`
- **Signing:** HMAC-SHA256 of `timestamp + method + path + body`, base64 encoded
- **Headers:** `OK-ACCESS-KEY`, `OK-ACCESS-SIGN`, `OK-ACCESS-TIMESTAMP`, `OK-ACCESS-PASSPHRASE`
- **Endpoints:**
  - `POST /api/v5/trade/order` (place order, body: `{"instId":"...","tdMode":"cross","side":"buy","sz":"10","ordType":"market"}`)
  - `GET /api/v5/account/positions?instId=...` (get position)
  - `GET /api/v5/trade/fills?instId=...` (get trades)

```go
package exchange

// Full implementation of OKXClient struct implementing Client interface.
// Constructor: NewOKXClient(apiKey, apiSecret, passphrase string) *OKXClient
```

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/internal/exchange/okx.go
git commit -m "feat(spread-arb): OKX exchange client"
```

---

## Task 6: Task Manager

**Files:**
- Create: `spread-arbitrage/internal/engine/task.go`
- Create: `spread-arbitrage/internal/engine/task_test.go`

- [ ] **Step 1: Write tests for TaskManager**

Test CRUD operations: Create, Get, List, Update, Delete. Test validation: `short_spread` requires `OpenThreshold > CloseThreshold`, `long_spread` requires `OpenThreshold < CloseThreshold`. Test that Delete of a running task returns an error.

```go
package engine

import (
    "testing"
    "spread-arbitrage/internal/model"
)

func TestTaskManager_CreateAndGet(t *testing.T) {
    // Create a task, verify it gets an ID, verify Get returns it
}

func TestTaskManager_List(t *testing.T) {
    // Create 3 tasks, verify List returns all 3
}

func TestTaskManager_Update(t *testing.T) {
    // Create, update threshold, verify change persisted
}

func TestTaskManager_Delete(t *testing.T) {
    // Create stopped task, delete succeeds
    // Create running task, delete returns error
}

func TestTaskManager_Validation_ShortSpread(t *testing.T) {
    // short_spread: OpenThreshold must be > CloseThreshold
    // Valid: open=0.5, close=0.1 → OK
    // Invalid: open=0.1, close=0.5 → error
}

func TestTaskManager_Validation_LongSpread(t *testing.T) {
    // long_spread: OpenThreshold must be < CloseThreshold
    // Valid: open=-0.5, close=-0.1 → OK
    // Invalid: open=-0.1, close=-0.5 → error
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
cd spread-arbitrage && go test ./internal/engine/ -v
```

Expected: compilation errors (types not defined yet).

- [ ] **Step 3: Implement `task.go`**

```go
package engine

import (
    "fmt"
    "sync"
    "spread-arbitrage/internal/model"
    "github.com/google/uuid"
)

type TaskManager struct {
    mu    sync.RWMutex
    tasks map[string]*model.Task
}

func NewTaskManager() *TaskManager { ... }
func (tm *TaskManager) Create(req model.TaskCreateRequest) (*model.Task, error) { ... }
func (tm *TaskManager) Get(id string) (*model.Task, error) { ... }
func (tm *TaskManager) List() []*model.Task { ... }
func (tm *TaskManager) Update(id string, req model.TaskCreateRequest) (*model.Task, error) { ... }
func (tm *TaskManager) Delete(id string) error { ... }
func (tm *TaskManager) SetStatus(id string, status model.TaskStatus) error { ... }
```

Validation in `Create` and `Update`:
- `short_spread`: `OpenThreshold > CloseThreshold`
- `long_spread`: `OpenThreshold < CloseThreshold`
- `ConfirmCount >= 1`
- `QuantityPerOrder > 0`, `MaxPositionQty > 0`
- `ExchangeA != ExchangeB`

- [ ] **Step 4: Run tests, verify they pass**

```bash
cd spread-arbitrage && go test ./internal/engine/ -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add spread-arbitrage/internal/engine/task.go spread-arbitrage/internal/engine/task_test.go
git commit -m "feat(spread-arb): TaskManager with CRUD and validation"
```

---

## Task 7: Spread Calculation & Confirm Counter

**Files:**
- Create: `spread-arbitrage/internal/engine/spread.go`
- Create: `spread-arbitrage/internal/engine/spread_test.go`

- [ ] **Step 1: Write tests**

```go
package engine

import "testing"

func TestCalcSpread_ShortSpread_OpenSignal(t *testing.T) {
    // short_spread: A.bid - B.ask > OpenThreshold → open signal
    // A.bid=100.5, B.ask=99.8, OpenThreshold=0.5
    // spread = 0.7 > 0.5 → should signal open
}

func TestCalcSpread_ShortSpread_CloseSignal(t *testing.T) {
    // short_spread: A.ask - B.bid < CloseThreshold → close signal
    // A.ask=100.1, B.bid=100.0, CloseThreshold=0.2
    // spread = 0.1 < 0.2 → should signal close
}

func TestCalcSpread_LongSpread_OpenSignal(t *testing.T) {
    // long_spread: A.ask - B.bid < OpenThreshold → open signal
    // A.ask=99.8, B.bid=100.5, OpenThreshold=-0.5
    // spread = -0.7 < -0.5 → should signal open
}

func TestCalcSpread_LongSpread_CloseSignal(t *testing.T) {
    // long_spread: A.bid - B.ask > CloseThreshold → close signal
    // A.bid=100.0, B.ask=99.9, CloseThreshold=-0.1
    // spread = 0.1 > -0.1 → should signal close
}

func TestConfirmCounter_TriggersAfterN(t *testing.T) {
    // ConfirmCount=3, feed 3 consecutive true → triggers
    cc := NewConfirmCounter(3)
    assert cc.Update(true) == false
    assert cc.Update(true) == false
    assert cc.Update(true) == true  // 3rd consecutive → fire
}

func TestConfirmCounter_ResetsOnFalse(t *testing.T) {
    // Feed 2 true, 1 false, 2 true → never triggers
    cc := NewConfirmCounter(3)
    cc.Update(true)
    cc.Update(true)
    cc.Update(false) // reset
    assert cc.Update(true) == false
    assert cc.Update(true) == false
}

func TestConfirmCounter_ResetsAfterTrigger(t *testing.T) {
    // After triggering, counter resets so it needs N more to trigger again
    cc := NewConfirmCounter(2)
    assert cc.Update(true) == false
    assert cc.Update(true) == true  // triggered
    assert cc.Update(true) == false // reset, counting again
    assert cc.Update(true) == true  // triggered again
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
cd spread-arbitrage && go test ./internal/engine/ -v -run "TestCalcSpread|TestConfirmCounter"
```

- [ ] **Step 3: Implement `spread.go`**

```go
package engine

import "spread-arbitrage/internal/model"

// Signal represents what action the engine should take
type Signal int

const (
    SignalNone  Signal = iota
    SignalOpen
    SignalClose
)

// ConfirmCounter tracks consecutive condition matches
type ConfirmCounter struct {
    required int
    current  int
}

func NewConfirmCounter(required int) *ConfirmCounter { ... }

// Update returns true when the counter reaches the required count.
// Resets on false input or after triggering.
func (c *ConfirmCounter) Update(conditionMet bool) bool { ... }
func (c *ConfirmCounter) Current() int { ... }
func (c *ConfirmCounter) Reset() { ... }

// SpreadEvaluator evaluates spread signals for a single task
type SpreadEvaluator struct {
    task         *model.Task
    openCounter  *ConfirmCounter
    closeCounter *ConfirmCounter
}

func NewSpreadEvaluator(task *model.Task) *SpreadEvaluator { ... }

// Evaluate takes two BookTickers (A and B) and returns a Signal.
// Returns SignalNone if data is stale (latency > DataMaxLatencyMs).
func (se *SpreadEvaluator) Evaluate(a, b model.BookTicker) Signal { ... }

// CurrentSpread returns the spread value used for display (based on direction).
func (se *SpreadEvaluator) CurrentSpread(a, b model.BookTicker) float64 { ... }

// Counters returns current open/close confirm counts for display.
func (se *SpreadEvaluator) Counters() (openCount, closeCount int) { ... }
```

Evaluate logic:
- Check latency: compare exchange-side timestamp vs local receive time: `a.ReceivedAt.Sub(a.Timestamp)` and `b.ReceivedAt.Sub(b.Timestamp)` must both be < `DataMaxLatencyMs`. Also check freshness: `time.Since(a.ReceivedAt)` < `DataMaxLatencyMs`. Both checks must pass for both sides. If not, return `SignalNone` and reset both counters.
- For `short_spread`:
  - Open check: `a.Bid - b.Ask > task.OpenThreshold`
  - Close check: `a.Ask - b.Bid < task.CloseThreshold`
- For `long_spread`:
  - Open check: `a.Ask - b.Bid < task.OpenThreshold`
  - Close check: `a.Bid - b.Ask > task.CloseThreshold`
- Feed open condition to openCounter, close condition to closeCounter.
- If openCounter fires → `SignalOpen`. If closeCounter fires → `SignalClose`.
- Both can't fire simultaneously because the threshold constraints make the conditions mutually exclusive.

- [ ] **Step 4: Run tests, verify they pass**

```bash
cd spread-arbitrage && go test ./internal/engine/ -v -run "TestCalcSpread|TestConfirmCounter"
```

- [ ] **Step 5: Commit**

```bash
git add spread-arbitrage/internal/engine/spread.go spread-arbitrage/internal/engine/spread_test.go
git commit -m "feat(spread-arb): spread evaluator with confirm counter"
```

---

## Task 8: WS Manager

**Files:**
- Create: `spread-arbitrage/internal/wsmanager/manager.go`

- [ ] **Step 1: Implement `manager.go`**

The WS Manager manages exchange WebSocket subscriptions and distributes BookTicker updates to the engine.

```go
package wsmanager

import (
    "context"
    "log"
    "sync"
    "spread-arbitrage/internal/exchange"
    "spread-arbitrage/internal/model"
)

// Manager manages WebSocket subscriptions across exchanges.
// It maintains latest BookTicker per (exchange, symbol) pair.
type Manager struct {
    mu       sync.RWMutex
    clients  map[string]exchange.Client          // exchange name → client
    latest   map[string]model.BookTicker         // "exchange:symbol" → latest ticker
    subs     map[string]context.CancelFunc       // "exchange:symbol" → cancel func
    onChange func(exchange, symbol string, tick model.BookTicker) // callback on new tick
}

func New(clients map[string]exchange.Client, onChange func(string, string, model.BookTicker)) *Manager { ... }

// Subscribe starts streaming BookTicker for a symbol on an exchange.
// Idempotent — if already subscribed, does nothing.
func (m *Manager) Subscribe(ctx context.Context, exchangeName, symbol string) error { ... }

// Unsubscribe stops the stream for a symbol on an exchange.
func (m *Manager) Unsubscribe(exchangeName, symbol string) { ... }

// GetLatest returns the most recent BookTicker for an exchange+symbol.
// Returns (ticker, true) if available, (zero, false) if not.
func (m *Manager) GetLatest(exchangeName, symbol string) (model.BookTicker, bool) { ... }

// Close stops all subscriptions.
func (m *Manager) Close() { ... }
```

The `Subscribe` method launches a goroutine that reads from the exchange client's `SubscribeBookTicker` channel, updates `latest`, and calls `onChange`. On channel close (WS disconnect), it logs and marks the exchange as stale (the reconnection happens inside the exchange client).

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/internal/wsmanager/
git commit -m "feat(spread-arb): WebSocket manager for exchange subscriptions"
```

---

## Task 9: Engine (Core Trading Loop)

**Files:**
- Create: `spread-arbitrage/internal/engine/engine.go`
- Create: `spread-arbitrage/internal/engine/engine_test.go`

- [ ] **Step 1: Write tests**

Test the engine's trade execution logic using a mock exchange client:

```go
package engine

import "testing"

// MockClient implements exchange.Client for testing
// Records all PlaceMarketOrder calls, returns configurable results

func TestEngine_ShortSpread_OpensPosition(t *testing.T) {
    // Setup: task with short_spread, ConfirmCount=1
    // Feed BookTickers where A.bid - B.ask > OpenThreshold
    // Verify: PlaceMarketOrder called with SELL on A, BUY on B
}

func TestEngine_ShortSpread_ClosesPosition(t *testing.T) {
    // Setup: task with short_spread, already has position
    // Feed BookTickers where A.ask - B.bid < CloseThreshold
    // Verify: PlaceMarketOrder called with BUY on A, SELL on B
}

func TestEngine_RespectsMaxPosition(t *testing.T) {
    // Setup: MaxPositionQty=10, QuantityPerOrder=5
    // Open twice (fills to 10), third signal should NOT open
}

func TestEngine_StaleData_NoTrade(t *testing.T) {
    // Feed BookTicker with old timestamp
    // Verify: no orders placed
}

func TestEngine_SingleSideFail_ClosesOtherSide(t *testing.T) {
    // Mock: A order succeeds, B order fails
    // Verify: engine calls close on A, stops the task
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
cd spread-arbitrage && go test ./internal/engine/ -v -run "TestEngine_"
```

- [ ] **Step 3: Implement `engine.go`**

```go
package engine

import (
    "context"
    "log"
    "sync"
    "spread-arbitrage/internal/exchange"
    "spread-arbitrage/internal/model"
    "spread-arbitrage/internal/wsmanager"
)

// EventCallback is called when notable events happen (for WS push to frontend)
type EventCallback func(event model.WSEvent)

type Engine struct {
    taskMgr    *TaskManager
    wsMgr      *wsmanager.Manager
    clients    map[string]exchange.Client
    evaluators map[string]*SpreadEvaluator // task ID → evaluator
    positions  map[string]float64           // task ID → current position qty
    mu         sync.RWMutex
    onEvent    EventCallback
}

func NewEngine(taskMgr *TaskManager, wsMgr *wsmanager.Manager, clients map[string]exchange.Client, onEvent EventCallback) *Engine { ... }

// OnTick is called by WS Manager whenever a new BookTicker arrives.
// It checks all running tasks that involve this exchange+symbol.
func (e *Engine) OnTick(exchangeName, symbol string, tick model.BookTicker) { ... }

// StartTask begins auto-trading for a task. Subscribes to WS feeds.
func (e *Engine) StartTask(ctx context.Context, taskID string) error { ... }

// StopTask stops auto-trading for a task.
func (e *Engine) StopTask(taskID string) error { ... }

// ManualOpen triggers one open order for a task (ignores thresholds and counters).
func (e *Engine) ManualOpen(ctx context.Context, taskID string) error { ... }

// ManualClose triggers one close order for a task.
func (e *Engine) ManualClose(ctx context.Context, taskID string) error { ... }

// executeOpen places both legs of an open trade.
func (e *Engine) executeOpen(ctx context.Context, task *model.Task) error { ... }

// executeClose places both legs of a close trade.
func (e *Engine) executeClose(ctx context.Context, task *model.Task) error { ... }

// GetPositionQty returns the current tracked position quantity for a task.
func (e *Engine) GetPositionQty(taskID string) float64 { ... }
```

`OnTick` logic:
1. Find all running tasks that match this exchange+symbol.
2. For each task, get latest tickers for both exchanges from WS Manager.
3. If either ticker is missing, skip.
4. Call `evaluator.Evaluate(tickA, tickB)`.
5. Broadcast `SpreadUpdate` event via `onEvent`.
6. If `SignalOpen` and `positions[taskID] + QuantityPerOrder <= MaxPositionQty` → `executeOpen`.
7. If `SignalClose` and `positions[taskID] > 0` → `executeClose`.

`executeOpen` / `executeClose`:
1. Determine sides based on direction and action.
2. Place both orders concurrently (two goroutines).
3. If both succeed: update `positions[taskID]`, emit `trade_executed` event.
4. If one fails: attempt to close the successful side, emit `error` event, stop the task.

- [ ] **Step 4: Run tests, verify they pass**

```bash
cd spread-arbitrage && go test ./internal/engine/ -v -run "TestEngine_"
```

- [ ] **Step 5: Commit**

```bash
git add spread-arbitrage/internal/engine/engine.go spread-arbitrage/internal/engine/engine_test.go
git commit -m "feat(spread-arb): core trading engine with position management"
```

---

## Task 10: API Server & REST Routes

**Files:**
- Create: `spread-arbitrage/internal/api/server.go`
- Create: `spread-arbitrage/internal/api/routes.go`
- Create: `spread-arbitrage/internal/api/ws.go`

- [ ] **Step 1: Create `ws.go` — WebSocket hub**

```go
package api

import (
    "sync"
    "github.com/gorilla/websocket"
    "spread-arbitrage/internal/model"
)

// Hub manages connected WebSocket clients and broadcasts events.
type Hub struct {
    mu      sync.RWMutex
    clients map[*websocket.Conn]bool
}

func NewHub() *Hub { ... }
func (h *Hub) Register(conn *websocket.Conn) { ... }
func (h *Hub) Unregister(conn *websocket.Conn) { ... }
func (h *Hub) Broadcast(event model.WSEvent) { ... }
```

- [ ] **Step 2: Create `routes.go` — REST handlers**

```go
package api

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "spread-arbitrage/internal/engine"
    "spread-arbitrage/internal/exchange"
    "spread-arbitrage/internal/model"
)

type Handler struct {
    engine  *engine.Engine
    taskMgr *engine.TaskManager
    clients map[string]exchange.Client
    hub     *Hub
}

func NewHandler(eng *engine.Engine, tm *engine.TaskManager, clients map[string]exchange.Client, hub *Hub) *Handler { ... }

// RegisterRoutes sets up all API routes on the gin engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
    api := r.Group("/api")
    api.GET("/tasks", h.ListTasks)
    api.POST("/tasks", h.CreateTask)
    api.PUT("/tasks/:id", h.UpdateTask)
    api.DELETE("/tasks/:id", h.DeleteTask)
    api.POST("/tasks/:id/start", h.StartTask)
    api.POST("/tasks/:id/stop", h.StopTask)
    api.POST("/tasks/:id/open", h.ManualOpen)
    api.POST("/tasks/:id/close", h.ManualClose)
    api.GET("/tasks/:id/positions", h.GetPositions)
    api.GET("/tasks/:id/trades", h.GetTrades)

    r.GET("/ws", h.HandleWebSocket)
}

func (h *Handler) ListTasks(c *gin.Context) { ... }
func (h *Handler) CreateTask(c *gin.Context) { ... }
func (h *Handler) UpdateTask(c *gin.Context) { ... }
func (h *Handler) DeleteTask(c *gin.Context) { ... }
func (h *Handler) StartTask(c *gin.Context) { ... }
func (h *Handler) StopTask(c *gin.Context) { ... }
func (h *Handler) ManualOpen(c *gin.Context) { ... }
func (h *Handler) ManualClose(c *gin.Context) { ... }

// GetPositions queries both exchanges for the task's symbol positions.
func (h *Handler) GetPositions(c *gin.Context) { ... }

// GetTrades queries both exchanges for the task's symbol trades.
func (h *Handler) GetTrades(c *gin.Context) { ... }

// HandleWebSocket upgrades to WS and registers with hub.
func (h *Handler) HandleWebSocket(c *gin.Context) { ... }
```

Handler details:
- `CreateTask`: parse `TaskCreateRequest`, call `taskMgr.Create`, return 201.
- `StartTask`: call `engine.StartTask`, return 200.
- `StopTask`: call `engine.StopTask`, return 200.
- `ManualOpen`/`ManualClose`: call `engine.ManualOpen`/`engine.ManualClose`.
- `GetPositions`: call `clients[exchangeA].GetPosition(symbol)` and `clients[exchangeB].GetPosition(symbol)`, return both.
- `GetTrades`: call `clients[exchangeA].GetTrades(symbol)` and `clients[exchangeB].GetTrades(symbol)`, merge and sort by time.

- [ ] **Step 3: Create `server.go`**

```go
package api

import (
    "github.com/gin-contrib/cors"
    "github.com/gin-gonic/gin"
    "spread-arbitrage/internal/engine"
    "spread-arbitrage/internal/exchange"
)

// NewServer creates and configures the gin server.
// Serves React static files from web/dist/ at root.
// Serves API routes under /api/.
func NewServer(eng *engine.Engine, tm *engine.TaskManager, clients map[string]exchange.Client, hub *Hub) *gin.Engine {
    r := gin.Default()

    // CORS middleware for development
    r.Use(cors.New(cors.Config{
        AllowOrigins:     []string{"http://localhost:5173"},
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type"},
        AllowWebSockets:  true,
    }))

    handler := NewHandler(eng, tm, clients, hub)
    handler.RegisterRoutes(r)

    // Serve React frontend (production build)
    r.Static("/assets", "./web/dist/assets")
    r.StaticFile("/", "./web/dist/index.html")
    r.NoRoute(func(c *gin.Context) {
        c.File("./web/dist/index.html")
    })

    return r
}
```

- [ ] **Step 4: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add spread-arbitrage/internal/api/
git commit -m "feat(spread-arb): API server with REST routes and WebSocket hub"
```

---

## Task 11: Wire Up `main.go`

**Files:**
- Modify: `spread-arbitrage/main.go`

- [ ] **Step 1: Update `main.go` to wire all components**

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "spread-arbitrage/internal/api"
    "spread-arbitrage/internal/engine"
    "spread-arbitrage/internal/exchange"
    "spread-arbitrage/internal/model"
    "spread-arbitrage/internal/wsmanager"
)

func main() {
    cfg := LoadConfig()

    // Create exchange clients
    clients := map[string]exchange.Client{
        "binance": exchange.NewBinanceClient(cfg.Binance.APIKey, cfg.Binance.APISecret),
        "aster":   exchange.NewAsterClient(cfg.Aster.APIKey, cfg.Aster.APISecret),
        "okx":     exchange.NewOKXClient(cfg.OKX.APIKey, cfg.OKX.APISecret, cfg.OKX.Passphrase),
    }

    // Create components — use a channel-based approach to break the circular dependency
    // between WS Manager and Engine (WS Manager calls Engine.OnTick, Engine needs WS Manager)
    hub := api.NewHub()
    taskMgr := engine.NewTaskManager()

    // Create WS Manager with a placeholder onChange that we'll wire after creating Engine
    var eng *engine.Engine
    wsMgr := wsmanager.New(clients, func(exchangeName, symbol string, tick model.BookTicker) {
        if eng != nil {
            eng.OnTick(exchangeName, symbol, tick)
        }
    })
    eng = engine.NewEngine(taskMgr, wsMgr, clients, func(event model.WSEvent) {
        hub.Broadcast(event)
    })

    // Start HTTP server
    server := api.NewServer(eng, taskMgr, clients, hub)
    go func() {
        log.Printf("Server listening on :%s", cfg.Port)
        if err := server.Run(":" + cfg.Port); err != nil {
            log.Fatal(err)
        }
    }()

    // Graceful shutdown
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit
    log.Println("Shutting down...")
    wsMgr.Close()
    for _, c := range clients {
        c.Close()
    }
}
```

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/main.go
git commit -m "feat(spread-arb): wire up main.go with all components"
```

---

## Task 12: React Frontend — Project Setup

**Files:**
- Create: `spread-arbitrage/web/package.json`
- Create: `spread-arbitrage/web/tsconfig.json`
- Create: `spread-arbitrage/web/vite.config.ts`
- Create: `spread-arbitrage/web/index.html`
- Create: `spread-arbitrage/web/src/main.tsx`
- Create: `spread-arbitrage/web/src/App.tsx`
- Create: `spread-arbitrage/web/src/api.ts`

- [ ] **Step 1: Scaffold React project with Vite**

```bash
cd spread-arbitrage/web
npm create vite@latest . -- --template react-ts
npm install
```

- [ ] **Step 2: Configure Vite proxy for development**

Update `vite.config.ts` to proxy `/api` and `/ws` to the Go backend at `localhost:8080`:

```typescript
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': {
        target: 'ws://localhost:8080',
        ws: true,
      },
    },
  },
})
```

- [ ] **Step 3: Create `api.ts` — REST client**

```typescript
const BASE = '/api'

export interface Task {
  id: string
  symbol: string
  exchange_a: string
  exchange_b: string
  direction: 'long_spread' | 'short_spread'
  status: 'running' | 'stopped'
  open_threshold: number
  close_threshold: number
  confirm_count: number
  quantity_per_order: number
  max_position_qty: number
  data_max_latency_ms: number
}

export interface TaskCreateRequest {
  symbol: string
  exchange_a: string
  exchange_b: string
  direction: 'long_spread' | 'short_spread'
  open_threshold: number
  close_threshold: number
  confirm_count: number
  quantity_per_order: number
  max_position_qty: number
  data_max_latency_ms: number
}

export interface Position {
  exchange: string
  symbol: string
  side: string
  size: number
  entry_price: number
  unrealized_pnl: number
}

export interface Trade {
  exchange: string
  symbol: string
  side: string
  quantity: number
  price: number
  fee: number
  timestamp: string
  order_id: string
}

export async function listTasks(): Promise<Task[]> { ... }
export async function createTask(req: TaskCreateRequest): Promise<Task> { ... }
export async function updateTask(id: string, req: TaskCreateRequest): Promise<Task> { ... }
export async function deleteTask(id: string): Promise<void> { ... }
export async function startTask(id: string): Promise<void> { ... }
export async function stopTask(id: string): Promise<void> { ... }
export async function manualOpen(id: string): Promise<void> { ... }
export async function manualClose(id: string): Promise<void> { ... }
export async function getPositions(id: string): Promise<Position[]> { ... }
export async function getTrades(id: string): Promise<Trade[]> { ... }
```

- [ ] **Step 4: Create minimal `App.tsx`**

Just renders "Spread Arbitrage" heading for now. Components added in next tasks.

- [ ] **Step 5: Verify frontend builds**

```bash
cd spread-arbitrage/web && npm run build
```

- [ ] **Step 6: Commit**

```bash
git add spread-arbitrage/web/
git commit -m "feat(spread-arb): React frontend scaffolding with API client"
```

---

## Task 13: React Frontend — WebSocket Hook

**Files:**
- Create: `spread-arbitrage/web/src/hooks/useWebSocket.ts`

- [ ] **Step 1: Create `useWebSocket.ts`**

```typescript
import { useEffect, useRef, useCallback, useState } from 'react'

export interface WSEvent {
  type: 'spread_update' | 'task_status' | 'trade_executed' | 'error'
  task_id: string
  data: any
}

export function useWebSocket(onMessage: (event: WSEvent) => void) {
  const [connected, setConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number>()

  const connect = useCallback(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws`)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => {
      setConnected(false)
      reconnectTimer.current = window.setTimeout(connect, 3000)
    }
    ws.onmessage = (e) => {
      const event: WSEvent = JSON.parse(e.data)
      onMessage(event)
    }
  }, [onMessage])

  useEffect(() => {
    connect()
    return () => {
      clearTimeout(reconnectTimer.current)
      wsRef.current?.close()
    }
  }, [connect])

  return { connected }
}
```

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage/web && npm run build
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/web/src/hooks/
git commit -m "feat(spread-arb): WebSocket hook with auto-reconnect"
```

---

## Task 14: React Frontend — TaskForm Component

**Files:**
- Create: `spread-arbitrage/web/src/components/TaskForm.tsx`

- [ ] **Step 1: Create `TaskForm.tsx`**

A form for creating and editing tasks. Fields:
- Symbol (text input)
- Exchange A / Exchange B (dropdowns: binance, aster, okx)
- Direction (dropdown: long_spread, short_spread)
- Open Threshold (number input)
- Close Threshold (number input)
- Confirm Count (number input)
- Quantity Per Order (number input)
- Max Position Qty (number input)
- Data Max Latency Ms (number input, default 1000)
- Submit button (Create / Update)
- Cancel button

Props: `onSubmit(req: TaskCreateRequest)`, `initialValues?: Task`, `onCancel?: () => void`

Client-side validation:
- ExchangeA != ExchangeB
- short_spread: OpenThreshold > CloseThreshold
- long_spread: OpenThreshold < CloseThreshold
- ConfirmCount >= 1
- QuantityPerOrder > 0, MaxPositionQty > 0

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage/web && npm run build
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/web/src/components/TaskForm.tsx
git commit -m "feat(spread-arb): TaskForm component with validation"
```

---

## Task 15: React Frontend — TaskList & TaskDetail Components

**Files:**
- Create: `spread-arbitrage/web/src/components/TaskList.tsx`
- Create: `spread-arbitrage/web/src/components/TaskDetail.tsx`

- [ ] **Step 1: Create `TaskDetail.tsx`**

Displays for a single task (always expanded):
- Current spread (from WS updates), open/close confirm counters
- Action buttons: Start, Stop, Manual Open, Manual Close, Edit, Delete
- Positions table: exchange, side, size, entry price, unrealized PnL (fetched from REST)
- Trades table: time, exchange, side, qty, price, fee (fetched from REST)

Props: `task: Task`, `spreadData?: SpreadUpdate`, event handlers for actions.

- [ ] **Step 2: Create `TaskList.tsx`**

Renders a list of `TaskDetail` components, one per task, all expanded. Plus a button to create a new task (shows `TaskForm`).

Props: receives tasks, spread updates, and action handlers.

- [ ] **Step 3: Verify build**

```bash
cd spread-arbitrage/web && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add spread-arbitrage/web/src/components/
git commit -m "feat(spread-arb): TaskList and TaskDetail components"
```

---

## Task 16: React Frontend — Wire Up App.tsx

**Files:**
- Modify: `spread-arbitrage/web/src/App.tsx`

- [ ] **Step 1: Update `App.tsx`**

Wire all components together:
- State: `tasks: Task[]`, `spreadUpdates: Map<taskId, SpreadUpdate>`
- On mount: fetch tasks via `listTasks()`
- WebSocket: use `useWebSocket` hook, update spread data and task statuses on events
- Render: `TaskList` with all state and handlers
- Handlers call the `api.ts` functions and refresh task list on success

```typescript
import { useState, useEffect, useCallback } from 'react'
import { useWebSocket, WSEvent } from './hooks/useWebSocket'
import * as api from './api'
import { TaskList } from './components/TaskList'

function App() {
  const [tasks, setTasks] = useState<api.Task[]>([])
  const [spreads, setSpreads] = useState<Map<string, any>>(new Map())

  const refreshTasks = async () => { setTasks(await api.listTasks()) }

  const handleWS = useCallback((event: WSEvent) => {
    if (event.type === 'spread_update') {
      setSpreads(prev => new Map(prev).set(event.task_id, event.data))
    }
    if (event.type === 'task_status') {
      refreshTasks()
    }
  }, [])

  const { connected } = useWebSocket(handleWS)

  useEffect(() => { refreshTasks() }, [])

  return (
    <div>
      <h1>Spread Arbitrage</h1>
      <div>WS: {connected ? 'Connected' : 'Disconnected'}</div>
      <TaskList
        tasks={tasks}
        spreads={spreads}
        onRefresh={refreshTasks}
        // ... pass all action handlers
      />
    </div>
  )
}
```

- [ ] **Step 2: Verify build**

```bash
cd spread-arbitrage/web && npm run build
```

- [ ] **Step 3: Commit**

```bash
git add spread-arbitrage/web/src/App.tsx
git commit -m "feat(spread-arb): wire up App.tsx with state management"
```

---

## Task 17: Integration — End-to-End Smoke Test

**Files:** No new files. This task verifies the full system works.

- [ ] **Step 1: Build frontend**

```bash
cd spread-arbitrage/web && npm run build
```

- [ ] **Step 2: Build backend**

```bash
cd spread-arbitrage && go build -o spread-arbitrage .
```

- [ ] **Step 3: Create a `.env` for testing**

```bash
cd spread-arbitrage
cp .env.example .env
# Fill in at least one exchange's API keys for testing
```

- [ ] **Step 4: Start the server**

```bash
cd spread-arbitrage && ./spread-arbitrage
```

Verify: "Server listening on :8080" in logs.

- [ ] **Step 5: Manual API testing**

```bash
# Create a task
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"symbol":"RAVEUSDT","exchange_a":"binance","exchange_b":"aster","direction":"short_spread","open_threshold":0.5,"close_threshold":0.1,"confirm_count":3,"quantity_per_order":100,"max_position_qty":500,"data_max_latency_ms":1000}'

# List tasks
curl http://localhost:8080/api/tasks

# Start the task (use the ID from create response)
curl -X POST http://localhost:8080/api/tasks/{id}/start

# Open browser to http://localhost:8080 — verify React UI loads and shows the task
```

- [ ] **Step 6: Verify WS updates in browser**

Open browser dev tools → Network → WS tab. Verify spread_update events are streaming when the task is running.

- [ ] **Step 7: Commit any fixes**

```bash
git add -A spread-arbitrage/
git commit -m "fix(spread-arb): integration fixes from smoke test"
```
