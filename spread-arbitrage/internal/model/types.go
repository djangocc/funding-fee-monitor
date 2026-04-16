package model

import "time"

// BookTicker represents best bid/ask from an exchange
type BookTicker struct {
	Exchange   string
	Symbol     string
	Bid        float64
	Ask        float64
	Timestamp  time.Time // exchange-side timestamp
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
	Exchange  string    `json:"exchange"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"` // "BUY" or "SELL"
	Quantity  float64   `json:"quantity"`
	Price     float64   `json:"price"` // fill price
	OrderID   string    `json:"order_id"`
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
	Spread       float64 `json:"spread"`
	OpenCounter  int     `json:"open_counter"`
	CloseCounter int     `json:"close_counter"`
}
