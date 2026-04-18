package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
)

// MockClient implements exchange.Client for testing
type MockClient struct {
	name     string
	mu       sync.Mutex
	orders   []mockOrder
	orderErr error
	position *model.Position
	orderCh  chan struct{} // signals when an order is placed
}

type mockOrder struct {
	Symbol        string
	Side          string
	Quantity      float64
	ClientOrderID string
}

func newMockClient(name string) *MockClient {
	return &MockClient{name: name, orderCh: make(chan struct{}, 100)}
}

func (m *MockClient) Name() string { return m.name }

func (m *MockClient) SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error) {
	return make(chan model.BookTicker), nil
}

func (m *MockClient) SubscribeDepth(ctx context.Context, symbol string) (<-chan model.OrderBook, error) {
	return make(chan model.OrderBook), nil
}

func (m *MockClient) SubscribeUserData(ctx context.Context, callbacks exchange.UserDataCallbacks) error {
	return nil
}

func (m *MockClient) PlaceMarketOrder(ctx context.Context, symbol, side string, qty float64, clientOrderID string) (*model.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.orderErr != nil {
		if m.orderCh != nil {
			m.orderCh <- struct{}{}
		}
		return nil, m.orderErr
	}
	m.orders = append(m.orders, mockOrder{Symbol: symbol, Side: side, Quantity: qty, ClientOrderID: clientOrderID})
	if m.orderCh != nil {
		m.orderCh <- struct{}{}
	}
	return &model.Order{
		Exchange:      m.name,
		Symbol:        symbol,
		Side:          side,
		Quantity:      qty,
		Price:         100.0,
		OrderID:       "test-order",
		ClientOrderID: clientOrderID,
		Timestamp:     time.Now(),
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

func (m *MockClient) GetOrders(ctx context.Context, symbol string) ([]model.Order, error) {
	return nil, nil
}

func (m *MockClient) GetFundingRate(ctx context.Context, symbol string) (*model.FundingRate, error) {
	return nil, nil
}

func (m *MockClient) Close() error { return nil }

func (m *MockClient) SetMockPosition(size float64, side string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.position = &model.Position{Exchange: m.name, Side: side, Size: size}
}

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

// waitForOrders waits until both mock clients have received orders
func waitForOrders(clientA, clientB *MockClient, timeout time.Duration) bool {
	deadline := time.After(timeout)
	gotA, gotB := false, false
	for !gotA || !gotB {
		select {
		case <-clientA.orderCh:
			gotA = true
		case <-clientB.orderCh:
			gotB = true
		case <-deadline:
			return false
		}
	}
	return true
}

func makeTestEngine(clientA, clientB *MockClient, onEvent EventCallback) (*Engine, *TaskManager) {
	tm := NewTaskManager()
	clients := map[string]interface{}{
		clientA.name: clientA,
		clientB.name: clientB,
	}
	eng := NewEngine(tm, nil, clients, nil, onEvent)
	return eng, tm
}

func TestEngine_ShortSpread_OpensPosition(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	var eventMu sync.Mutex
	var events []model.WSEvent
	onEvent := func(event model.WSEvent) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}

	eng, tm := makeTestEngine(clientA, clientB, onEvent)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
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

	// Wait for async order execution
	if !waitForOrders(clientA, clientB, 2*time.Second) {
		t.Fatal("Timed out waiting for orders")
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
}

func TestEngine_ShortSpread_ClosesPosition(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, tm := makeTestEngine(clientA, clientB, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Set positions before start so initial sync picks them up
	clientA.SetMockPosition(0.01, "SHORT")
	clientB.SetMockPosition(0.01, "LONG")

	ctx := context.Background()
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50005.0, Ask: 50006.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 50005.5, Ask: 50006.5, Timestamp: now, ReceivedAt: now}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalClose {
		t.Errorf("Expected SignalClose, got %v", signal)
	}

	if !waitForOrders(clientA, clientB, 2*time.Second) {
		t.Fatal("Timed out waiting for orders")
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

func TestEngine_StaleData_NoTrade(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, tm := makeTestEngine(clientA, clientB, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
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

func TestEngine_Unstable_DefersSignal(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, tm := makeTestEngine(clientA, clientB, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	ctx := context.Background()
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	// Make inflight unstable
	eng.inflight.Add(&InflightOrder{
		TriggerID: "existing", TaskID: task.ID,
		LegA: LegPending, LegB: LegPending,
		CidA: "existing-a", CidB: "existing-b",
		CreatedAt: time.Now(),
	})

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50010.0, Ask: 50011.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 49999.0, Ask: 50000.0, Timestamp: now, ReceivedAt: now}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalOpen {
		t.Errorf("Expected SignalOpen (signal detected but deferred), got %v", signal)
	}

	// No new orders should be placed
	time.Sleep(100 * time.Millisecond)
	if clientA.OrderCount() != 0 {
		t.Errorf("Expected 0 orders when unstable, got %d", clientA.OrderCount())
	}
}

func TestEngine_ManualOpen_BlockedWhenUnstable(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, tm := makeTestEngine(clientA, clientB, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	eng.inflight.Add(&InflightOrder{
		TriggerID: "existing", TaskID: task.ID,
		LegA: LegPending, LegB: LegPending,
		CidA: "existing-a", CidB: "existing-b",
		CreatedAt: time.Now(),
	})

	ctx := context.Background()
	err = eng.ManualOpen(ctx, task.ID)
	if err == nil {
		t.Fatal("Expected error when unstable")
	}
}

func TestEngine_ManualClose_BlockedWhenUnstable(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, tm := makeTestEngine(clientA, clientB, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	eng.inflight.Add(&InflightOrder{
		TriggerID: "existing", TaskID: task.ID,
		LegA: LegPending, LegB: LegPending,
		CidA: "existing-a", CidB: "existing-b",
		CreatedAt: time.Now(),
	})

	ctx := context.Background()
	err = eng.ManualClose(ctx, task.ID)
	if err == nil {
		t.Fatal("Expected error when unstable")
	}
}

func TestEngine_SingleSideFail_StopsTask(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	var eventMu sync.Mutex
	var events []model.WSEvent
	onEvent := func(event model.WSEvent) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}

	eng, tm := makeTestEngine(clientA, clientB, onEvent)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
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
		t.Errorf("Expected SignalOpen, got %v", signal)
	}

	// Wait for async execution
	time.Sleep(500 * time.Millisecond)

	updatedTask, err := tm.Get(task.ID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}
	if updatedTask.Status != model.StatusStopped {
		t.Errorf("Expected task status stopped, got %s", updatedTask.Status)
	}

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
}

func TestEngine_NoPositionNoClose(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, tm := makeTestEngine(clientA, clientB, nil)

	task, err := tm.Create(model.TaskCreateRequest{
		Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "bybit",
		Direction: model.ShortSpread, OpenThreshold: 5.0, CloseThreshold: 1.0,
		ConfirmCount: 1, QuantityPerOrder: 0.01, MaxPositionQty: 0.1, DataMaxLatencyMs: 1000,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	ctx := context.Background()
	if err := eng.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("Failed to start task: %v", err)
	}

	now := time.Now()
	tickA := model.BookTicker{Exchange: "binance", Symbol: "BTCUSDT", Bid: 50005.0, Ask: 50006.0, Timestamp: now, ReceivedAt: now}
	tickB := model.BookTicker{Exchange: "bybit", Symbol: "BTCUSDT", Bid: 50005.5, Ask: 50006.5, Timestamp: now, ReceivedAt: now}

	signal := eng.ProcessTick(ctx, task.ID, tickA, tickB)
	if signal != SignalClose {
		t.Errorf("Expected SignalClose, got %v", signal)
	}

	time.Sleep(100 * time.Millisecond)
	if clientA.OrderCount() != 0 {
		t.Errorf("Expected 0 orders when no position, got %d", clientA.OrderCount())
	}
}

func TestEngine_OnOrderUpdate_ResolvesInflight(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, _ := makeTestEngine(clientA, clientB, nil)

	eng.inflight.Add(&InflightOrder{
		TriggerID: "t1", TaskID: "task1",
		LegA: LegPending, LegB: LegPending,
		CidA: "sa-t1-A", CidB: "sa-t1-B",
		CreatedAt: time.Now(),
	})

	if eng.inflight.IsStable() {
		t.Fatal("should be unstable")
	}

	eng.OnOrderUpdate(model.OrderUpdate{
		Exchange: "binance", Symbol: "BTCUSDT", ClientOrderID: "sa-t1-A",
		Status: "FILLED", FilledQty: 1.0, AvgPrice: 100.0, Timestamp: time.Now(),
	})
	if eng.inflight.IsStable() {
		t.Fatal("should still be unstable after one leg")
	}

	eng.OnOrderUpdate(model.OrderUpdate{
		Exchange: "bybit", Symbol: "BTCUSDT", ClientOrderID: "sa-t1-B",
		Status: "FILLED", FilledQty: 1.0, AvgPrice: 100.0, Timestamp: time.Now(),
	})
	if !eng.inflight.IsStable() {
		t.Fatal("should be stable after both legs resolved")
	}
}

func TestEngine_OnAccountUpdate_UpdatesPosCache(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, _ := makeTestEngine(clientA, clientB, nil)

	now := time.Now()
	eng.OnAccountUpdate(model.AccountUpdate{
		Exchange: "binance", Symbol: "BTCUSDT", Side: "SHORT", Size: 5.0,
		Reason: "ORDER", Timestamp: now,
	})

	pos := eng.getPosFromCache("binance", "BTCUSDT")
	if pos != 5.0 {
		t.Fatalf("expected 5.0, got %.4f", pos)
	}

	// Stale event should be ignored
	eng.OnAccountUpdate(model.AccountUpdate{
		Exchange: "binance", Symbol: "BTCUSDT", Side: "SHORT", Size: 3.0,
		Reason: "ORDER", Timestamp: now.Add(-1 * time.Second),
	})
	pos = eng.getPosFromCache("binance", "BTCUSDT")
	if pos != 5.0 {
		t.Fatalf("stale event should not overwrite, expected 5.0, got %.4f", pos)
	}
}

func TestEngine_OnOrderUpdate_IgnoresNonTerminal(t *testing.T) {
	clientA := newMockClient("binance")
	clientB := newMockClient("bybit")

	eng, _ := makeTestEngine(clientA, clientB, nil)

	eng.inflight.Add(&InflightOrder{
		TriggerID: "t1", TaskID: "task1",
		LegA: LegPending, LegB: LegFilled,
		CidA: "sa-t1-A", CidB: "",
		CreatedAt: time.Now(),
	})

	// NEW status should be ignored
	eng.OnOrderUpdate(model.OrderUpdate{
		Exchange: "binance", ClientOrderID: "sa-t1-A",
		Status: "NEW", Timestamp: time.Now(),
	})
	if eng.inflight.IsStable() {
		t.Fatal("NEW should not resolve inflight")
	}

	// PARTIALLY_FILLED should be ignored
	eng.OnOrderUpdate(model.OrderUpdate{
		Exchange: "binance", ClientOrderID: "sa-t1-A",
		Status: "PARTIALLY_FILLED", FilledQty: 0.5, Timestamp: time.Now(),
	})
	if eng.inflight.IsStable() {
		t.Fatal("PARTIALLY_FILLED should not resolve inflight")
	}

	// FILLED should resolve
	eng.OnOrderUpdate(model.OrderUpdate{
		Exchange: "binance", ClientOrderID: "sa-t1-A",
		Status: "FILLED", FilledQty: 1.0, AvgPrice: 100.0, Timestamp: time.Now(),
	})
	if !eng.inflight.IsStable() {
		t.Fatal("FILLED should resolve inflight")
	}
}
