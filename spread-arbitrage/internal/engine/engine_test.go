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
	name     string
	mu       sync.Mutex
	orders   []mockOrder
	orderErr error // if set, PlaceMarketOrder returns this error
	position *model.Position
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

func (m *MockClient) PlaceMarketOrder(ctx context.Context, symbol, side string, qty float64, clientOrderID string) (*model.Order, error) {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.position != nil {
		return m.position, nil
	}
	return &model.Position{Exchange: m.name, Symbol: symbol, Size: 0}, nil
}

func (m *MockClient) SetMockPosition(size float64, side string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.position = &model.Position{Exchange: m.name, Side: side, Size: size}
}

func (m *MockClient) GetOrders(ctx context.Context, symbol string) ([]model.Order, error) {
	return nil, nil
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

func (m *MockClient) GetMockOrders() []mockOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockOrder, len(m.orders))
	copy(result, m.orders)
	return result
}

// TestEngine_ShortSpread_OpensPosition verifies that open signals trigger correct orders
func TestEngine_ShortSpread_OpensPosition(t *testing.T) {
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

	eng := NewEngine(tm, nil, clients, nil, onEvent)

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
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50010.0, Ask: 50011.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 49999.0, Ask: 50000.0, Timestamp: now, ReceivedAt: now}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen, got %v", signal)
	}

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

	orderB := clientB.LastOrder()
	if orderB.Side != "BUY" {
		t.Errorf("Expected BUY on exchange B, got %s", orderB.Side)
	}

	// After open, GetPositionQty queries exchange — mock returns Size=0 since we didn't update mock
	// But the real test is that orders were placed correctly
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
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	eng := NewEngine(tm, nil, clients, nil, nil)

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

	// Set mock positions BEFORE StartTask so initial sync picks them up
	clientA.SetMockPosition(0.01, "SHORT")
	clientB.SetMockPosition(0.01, "LONG")

	ctx := context.Background()
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50005.0, Ask: 50006.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 50005.5, Ask: 50006.5, Timestamp: now, ReceivedAt: now}

	// short_spread close: A.ask - B.bid = 50006 - 50005.5 = 0.5 < 1.0
	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalClose {
		t.Errorf("Expected SignalClose, got %v", signal)
	}

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
}

// TestEngine_RespectsMaxPosition verifies that engine respects MaxPositionQty from exchange
func TestEngine_RespectsMaxPosition(t *testing.T) {
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	eng := NewEngine(tm, nil, clients, nil, nil)

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
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50010.0, Ask: 50011.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 49999.0, Ask: 50000.0, Timestamp: now, ReceivedAt: now}

	// Mock starts at 0. After each successful open, refreshPositionCache queries mock,
	// so we need to update mock before the next tick.

	// First open: cache=0, 0 + 5 <= 10 → should work
	// But refreshPositionCache will query mock after open, so set mock to 5 for that
	// We use a trick: PlaceMarketOrder succeeds, then refreshPositionCache queries GetPosition.
	// We need mock to return 5 after the first open.
	// Solution: update mock position in a goroutine-safe way via a counter.

	// Actually simpler: just set mock position to what exchange would show BEFORE each tick,
	// since refreshPositionCache will read it after the trade.
	// For first tick: cache=0 (from StartTask), mock should return 5 after open
	clientA.SetMockPosition(5.0, "SHORT")
	clientB.SetMockPosition(5.0, "LONG")

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen for first trade, got %v", signal)
	}
	if clientA.OrderCount() != 1 {
		t.Errorf("Expected 1 order after first open, got %d", clientA.OrderCount())
	}
	// After refreshPositionCache, cache should now be 5
	if eng.GetPositionQty(task.ID) != 5.0 {
		t.Errorf("Expected cached position 5.0 after first open, got %f", eng.GetPositionQty(task.ID))
	}

	// Second open: cache=5, 5 + 5 <= 10 → should work
	// Set mock to 10 for post-trade refresh
	clientA.SetMockPosition(10.0, "SHORT")
	clientB.SetMockPosition(10.0, "LONG")

	signal = eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen for second trade, got %v", signal)
	}
	if clientA.OrderCount() != 2 {
		t.Errorf("Expected 2 orders after second open, got %d", clientA.OrderCount())
	}
	// After refresh, cache should be 10
	if eng.GetPositionQty(task.ID) != 10.0 {
		t.Errorf("Expected cached position 10.0 after second open, got %f", eng.GetPositionQty(task.ID))
	}

	// Third open attempt: cache=10, 10 + 5 > 10 → should NOT place orders
	signal = eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if clientA.OrderCount() != 2 {
		t.Errorf("Expected still 2 orders (max reached), got %d", clientA.OrderCount())
	}
}

// TestEngine_StaleData_NoTrade verifies that stale data does not trigger trades
func TestEngine_StaleData_NoTrade(t *testing.T) {
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	eng := NewEngine(tm, nil, clients, nil, nil)

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
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	staleTime := time.Now().Add(-2 * time.Second)
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50010.0, Ask: 50011.0, Timestamp: staleTime, ReceivedAt: staleTime}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 49999.0, Ask: 50000.0, Timestamp: staleTime, ReceivedAt: staleTime}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalNone {
		t.Errorf("Expected SignalNone for stale data, got %v", signal)
	}

	if clientA.OrderCount() != 0 {
		t.Errorf("Expected 0 orders with stale data, got %d", clientA.OrderCount())
	}
}

// TestEngine_SingleSideFail_StopsTask verifies error handling when one side fails
func TestEngine_SingleSideFail_StopsTask(t *testing.T) {
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

	eng := NewEngine(tm, nil, clients, nil, onEvent)

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
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	clientB.orderErr = errors.New("insufficient balance")

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50010.0, Ask: 50011.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 49999.0, Ask: 50000.0, Timestamp: now, ReceivedAt: now}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
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

	// Verify GetPositionQty returns 0 (exchange has no position)
	posQty := eng.GetPositionQty(task.ID)
	if posQty != 0.0 {
		t.Errorf("Expected position qty 0.0 after failed trade, got %f", posQty)
	}
}

// TestEngine_NoPositionNoClose verifies close signal is skipped when exchange has no position
func TestEngine_NoPositionNoClose(t *testing.T) {
	tm := NewTaskManager()
	clientA := &MockClient{name: "binance"}
	clientB := &MockClient{name: "bybit"}
	clients := map[string]interface{}{
		"binance": clientA,
		"bybit":   clientB,
	}

	eng := NewEngine(tm, nil, clients, nil, nil)

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
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Mock positions = 0 (no position on exchange)
	// Close signal should not place any orders

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50005.0, Ask: 50006.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 50005.5, Ask: 50006.5, Timestamp: now, ReceivedAt: now}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalClose {
		t.Errorf("Expected SignalClose, got %v", signal)
	}

	// No orders should be placed since exchange position is 0
	if clientA.OrderCount() != 0 {
		t.Errorf("Expected 0 orders when no position, got %d", clientA.OrderCount())
	}
}
