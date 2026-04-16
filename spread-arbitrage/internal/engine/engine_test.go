package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"spread-arbitrage/internal/model"
)

// MockClient implements exchange.Client for testing
type MockClient struct {
	name       string
	mu         sync.Mutex
	orders     []mockOrder
	orderErr   error // if set, PlaceMarketOrder returns this error
	position   *model.Position
	trades     []model.Trade
}

type mockOrder struct {
	Symbol   string
	Side     string
	Quantity float64
}

func (m *MockClient) Name() string { return m.name }

func (m *MockClient) SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error) {
	return make(chan model.BookTicker), nil
}

func (m *MockClient) PlaceMarketOrder(ctx context.Context, symbol, side string, qty float64) (*model.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.orderErr != nil {
		return nil, m.orderErr
	}
	m.orders = append(m.orders, mockOrder{Symbol: symbol, Side: side, Quantity: qty})
	return &model.Order{
		Exchange:  m.name,
		Symbol:    symbol,
		Side:      side,
		Quantity:  qty,
		Price:     100.0,
		OrderID:   "test-order",
		Timestamp: time.Now(),
	}, nil
}

func (m *MockClient) GetPosition(ctx context.Context, symbol string) (*model.Position, error) {
	if m.position != nil {
		return m.position, nil
	}
	return &model.Position{Exchange: m.name, Symbol: symbol}, nil
}

func (m *MockClient) GetTrades(ctx context.Context, symbol string) ([]model.Trade, error) {
	return m.trades, nil
}

func (m *MockClient) Close() error { return nil }

func (m *MockClient) OrderCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.orders)
}

func (m *MockClient) LastOrder() mockOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orders[len(m.orders)-1]
}

func (m *MockClient) GetOrders() []mockOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockOrder, len(m.orders))
	copy(result, m.orders)
	return result
}

// TestEngine_ShortSpread_OpensPosition verifies that open signals trigger correct orders
func TestEngine_ShortSpread_OpensPosition(t *testing.T) {
	// Setup
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	var eventMu sync.Mutex
	var events []model.WSEvent
	onEvent := func(event model.WSEvent) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}

	engine := NewEngine(tm, nil, clients, onEvent)

	// Create a short_spread task with ConfirmCount=1
	task, err := tm.Create(model.TaskCreateRequest{
		Symbol:           "BTCUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "bybit",
		Direction:        model.ShortSpread,
		OpenThreshold:    5.0,
		CloseThreshold:   1.0,
		ConfirmCount:     1,
		QuantityPerOrder: 0.01,
		MaxPositionQty:   0.1,
		DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Start the task
	ctx := context.Background()
	if err := engine.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Create tickers where A.bid - B.ask > OpenThreshold (5.0)
	now := time.Now()
	tickA := model.BookTicker{
		Exchange:   "binance",
		Symbol:     "BTCUSDT",
		Bid:        50010.0,
		Ask:        50011.0,
		Timestamp:  now,
		ReceivedAt: now,
	}
	tickB := model.BookTicker{
		Exchange:   "bybit",
		Symbol:     "BTCUSDT",
		Bid:        49999.0,
		Ask:        50000.0,
		Timestamp:  now,
		ReceivedAt: now,
	}

	// Process the tick (short_spread: A.bid - B.ask = 50010 - 50000 = 10 > 5.0)
	signal := engine.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen, got %v", signal)
	}

	// Verify: 2 orders placed (SELL on A, BUY on B)
	if clientA.OrderCount() != 1 {
		t.Errorf("Expected 1 order on binance, got %d", clientA.OrderCount())
	}
	if clientB.OrderCount() != 1 {
		t.Errorf("Expected 1 order on bybit, got %d", clientB.OrderCount())
	}

	orderA := clientA.LastOrder()
	if orderA.Side != "SELL" {
		t.Errorf("Expected SELL on exchange A, got %s", orderA.Side)
	}
	if orderA.Quantity != 0.01 {
		t.Errorf("Expected quantity 0.01, got %f", orderA.Quantity)
	}

	orderB := clientB.LastOrder()
	if orderB.Side != "BUY" {
		t.Errorf("Expected BUY on exchange B, got %s", orderB.Side)
	}

	// Verify position qty updated
	posQty := engine.GetPositionQty(task.ID)
	if posQty != 0.01 {
		t.Errorf("Expected position qty 0.01, got %f", posQty)
	}

	// Verify trade_executed event was emitted
	eventMu.Lock()
	found := false
	for _, evt := range events {
		if evt.Type == "trade_executed" && evt.TaskID == task.ID {
			found = true
			break
		}
	}
	eventMu.Unlock()
	if !found {
		t.Error("Expected trade_executed event to be emitted")
	}
}

// TestEngine_ShortSpread_ClosesPosition verifies that close signals trigger correct orders
func TestEngine_ShortSpread_ClosesPosition(t *testing.T) {
	// Setup
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	var eventMu sync.Mutex
	var events []model.WSEvent
	onEvent := func(event model.WSEvent) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}

	engine := NewEngine(tm, nil, clients, onEvent)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol:           "BTCUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "bybit",
		Direction:        model.ShortSpread,
		OpenThreshold:    5.0,
		CloseThreshold:   1.0,
		ConfirmCount:     1,
		QuantityPerOrder: 0.01,
		MaxPositionQty:   0.1,
		DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	ctx := context.Background()
	if err := engine.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Set existing position
	engine.SetPositionQty(task.ID, 0.01)

	// Create tickers where A.ask - B.bid < CloseThreshold (1.0)
	now := time.Now()
	tickA := model.BookTicker{
		Exchange:   "binance",
		Symbol:     "BTCUSDT",
		Bid:        50005.0,
		Ask:        50006.0,
		Timestamp:  now,
		ReceivedAt: now,
	}
	tickB := model.BookTicker{
		Exchange:   "bybit",
		Symbol:     "BTCUSDT",
		Bid:        50005.5,
		Ask:        50006.5,
		Timestamp:  now,
		ReceivedAt: now,
	}

	// Process the tick (short_spread close: A.ask - B.bid = 50006 - 50005.5 = 0.5 < 1.0)
	signal := engine.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalClose {
		t.Errorf("Expected SignalClose, got %v", signal)
	}

	// Verify: 2 orders placed (BUY on A, SELL on B) - opposite of open
	if clientA.OrderCount() != 1 {
		t.Errorf("Expected 1 order on binance, got %d", clientA.OrderCount())
	}
	if clientB.OrderCount() != 1 {
		t.Errorf("Expected 1 order on bybit, got %d", clientB.OrderCount())
	}

	orderA := clientA.LastOrder()
	if orderA.Side != "BUY" {
		t.Errorf("Expected BUY on exchange A for close, got %s", orderA.Side)
	}

	orderB := clientB.LastOrder()
	if orderB.Side != "SELL" {
		t.Errorf("Expected SELL on exchange B for close, got %s", orderB.Side)
	}

	// Verify position qty decreased
	posQty := engine.GetPositionQty(task.ID)
	if posQty != 0.0 {
		t.Errorf("Expected position qty 0.0, got %f", posQty)
	}
}

// TestEngine_RespectsMaxPosition verifies that engine respects MaxPositionQty
func TestEngine_RespectsMaxPosition(t *testing.T) {
	// Setup
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	engine := NewEngine(tm, nil, clients, nil)

	// MaxPositionQty=10, QuantityPerOrder=5
	task, err := tm.Create(model.TaskCreateRequest{
		Symbol:           "BTCUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "bybit",
		Direction:        model.ShortSpread,
		OpenThreshold:    5.0,
		CloseThreshold:   1.0,
		ConfirmCount:     1,
		QuantityPerOrder: 5.0,
		MaxPositionQty:   10.0,
		DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	ctx := context.Background()
	if err := engine.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Create tickers with open signal
	now := time.Now()
	tickA := model.BookTicker{
		Exchange:   "binance",
		Symbol:     "BTCUSDT",
		Bid:        50010.0,
		Ask:        50011.0,
		Timestamp:  now,
		ReceivedAt: now,
	}
	tickB := model.BookTicker{
		Exchange:   "bybit",
		Symbol:     "BTCUSDT",
		Bid:        49999.0,
		Ask:        50000.0,
		Timestamp:  now,
		ReceivedAt: now,
	}

	// First open: should work (0 + 5 <= 10)
	signal := engine.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen for first trade, got %v", signal)
	}
	if clientA.OrderCount() != 1 {
		t.Errorf("Expected 1 order after first open, got %d", clientA.OrderCount())
	}
	if engine.GetPositionQty(task.ID) != 5.0 {
		t.Errorf("Expected position 5.0 after first open, got %f", engine.GetPositionQty(task.ID))
	}

	// Second open: should work (5 + 5 <= 10)
	signal = engine.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen for second trade, got %v", signal)
	}
	if clientA.OrderCount() != 2 {
		t.Errorf("Expected 2 orders after second open, got %d", clientA.OrderCount())
	}
	if engine.GetPositionQty(task.ID) != 10.0 {
		t.Errorf("Expected position 10.0 after second open, got %f", engine.GetPositionQty(task.ID))
	}

	// Third open attempt: should NOT place orders (10 + 5 > 10)
	signal = engine.ProcessTick(ctx, task.ID, tickA, tickB)
	// Signal will be generated but orders won't be placed
	if clientA.OrderCount() != 2 {
		t.Errorf("Expected still 2 orders (max reached), got %d", clientA.OrderCount())
	}
	if engine.GetPositionQty(task.ID) != 10.0 {
		t.Errorf("Expected position still 10.0 (max reached), got %f", engine.GetPositionQty(task.ID))
	}
}

// TestEngine_StaleData_NoTrade verifies that stale data does not trigger trades
func TestEngine_StaleData_NoTrade(t *testing.T) {
	// Setup
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	engine := NewEngine(tm, nil, clients, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol:           "BTCUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "bybit",
		Direction:        model.ShortSpread,
		OpenThreshold:    5.0,
		CloseThreshold:   1.0,
		ConfirmCount:     1,
		QuantityPerOrder: 0.01,
		MaxPositionQty:   0.1,
		DataMaxLatencyMs: 1000, // 1 second max latency
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	ctx := context.Background()
	if err := engine.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Create stale tickers (2 seconds old)
	staleTime := time.Now().Add(-2 * time.Second)
	tickA := model.BookTicker{
		Exchange:   "binance",
		Symbol:     "BTCUSDT",
		Bid:        50010.0,
		Ask:        50011.0,
		Timestamp:  staleTime,
		ReceivedAt: staleTime,
	}
	tickB := model.BookTicker{
		Exchange:   "bybit",
		Symbol:     "BTCUSDT",
		Bid:        49999.0,
		Ask:        50000.0,
		Timestamp:  staleTime,
		ReceivedAt: staleTime,
	}

	// Process the tick - should return SignalNone due to staleness
	signal := engine.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalNone {
		t.Errorf("Expected SignalNone for stale data, got %v", signal)
	}

	// Verify no orders placed
	if clientA.OrderCount() != 0 {
		t.Errorf("Expected 0 orders with stale data, got %d", clientA.OrderCount())
	}
	if clientB.OrderCount() != 0 {
		t.Errorf("Expected 0 orders with stale data, got %d", clientB.OrderCount())
	}
}

// TestEngine_SingleSideFail_StopsTask verifies error handling when one side fails
func TestEngine_SingleSideFail_StopsTask(t *testing.T) {
	// Setup
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	var eventMu sync.Mutex
	var events []model.WSEvent
	onEvent := func(event model.WSEvent) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}

	engine := NewEngine(tm, nil, clients, onEvent)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol:           "BTCUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "bybit",
		Direction:        model.ShortSpread,
		OpenThreshold:    5.0,
		CloseThreshold:   1.0,
		ConfirmCount:     1,
		QuantityPerOrder: 0.01,
		MaxPositionQty:   0.1,
		DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	ctx := context.Background()
	if err := engine.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Set error on client B
	clientB.orderErr = errors.New("insufficient balance")

	// Create tickers with open signal
	now := time.Now()
	tickA := model.BookTicker{
		Exchange:   "binance",
		Symbol:     "BTCUSDT",
		Bid:        50010.0,
		Ask:        50011.0,
		Timestamp:  now,
		ReceivedAt: now,
	}
	tickB := model.BookTicker{
		Exchange:   "bybit",
		Symbol:     "BTCUSDT",
		Bid:        49999.0,
		Ask:        50000.0,
		Timestamp:  now,
		ReceivedAt: now,
	}

	// Process the tick - should fail
	signal := engine.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen to be generated, got %v", signal)
	}

	// Verify task status is stopped
	updatedTask, err := tm.Get(task.ID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}
	if updatedTask.Status != model.StatusStopped {
		t.Errorf("Expected task status stopped, got %s", updatedTask.Status)
	}

	// Verify error event was emitted
	eventMu.Lock()
	found := false
	for _, evt := range events {
		if evt.Type == "error" && evt.TaskID == task.ID {
			found = true
			break
		}
	}
	eventMu.Unlock()
	if !found {
		t.Error("Expected error event to be emitted")
	}

	// Verify position was not updated (trade failed)
	posQty := engine.GetPositionQty(task.ID)
	if posQty != 0.0 {
		t.Errorf("Expected position qty 0.0 after failed trade, got %f", posQty)
	}
}
