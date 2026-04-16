package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
	"spread-arbitrage/internal/wsmanager"
)

type EventCallback func(event model.WSEvent)

type Engine struct {
	taskMgr    *TaskManager
	wsMgr      *wsmanager.Manager
	clients    map[string]interface{} // Can be exchange.Client or test mock
	evaluators map[string]*SpreadEvaluator
	positions  map[string]float64 // task ID → current position qty
	mu         sync.RWMutex
	onEvent    EventCallback
}

func NewEngine(taskMgr *TaskManager, wsMgr *wsmanager.Manager, clients map[string]interface{}, onEvent EventCallback) *Engine {
	return &Engine{
		taskMgr:    taskMgr,
		wsMgr:      wsMgr,
		clients:    clients,
		evaluators: make(map[string]*SpreadEvaluator),
		positions:  make(map[string]float64),
		onEvent:    onEvent,
	}
}

// OnTick is called when a new BookTicker arrives from WS
func (e *Engine) OnTick(exchangeName, symbol string, tick model.BookTicker) {
	e.mu.RLock()
	tasks := e.taskMgr.List()
	e.mu.RUnlock()

	ctx := context.Background()

	for _, task := range tasks {
		if task.Status != model.StatusRunning {
			continue
		}

		// Check if this tick is relevant to this task
		if task.Symbol != symbol {
			continue
		}
		if task.ExchangeA != exchangeName && task.ExchangeB != exchangeName {
			continue
		}

		// Get latest tickers for both exchanges
		if e.wsMgr == nil {
			continue // Skip in test mode without WS manager
		}

		tickA, okA := e.wsMgr.GetLatest(task.ExchangeA, task.Symbol)
		tickB, okB := e.wsMgr.GetLatest(task.ExchangeB, task.Symbol)
		if !okA || !okB {
			continue
		}

		// Process the tick
		e.ProcessTick(ctx, task.ID, tickA, tickB)
	}
}

// ProcessTick evaluates tickers and executes trades if needed
func (e *Engine) ProcessTick(ctx context.Context, taskID string, tickA, tickB model.BookTicker) Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return SignalNone
	}

	if task.Status != model.StatusRunning {
		return SignalNone
	}

	evaluator, exists := e.evaluators[taskID]
	if !exists {
		return SignalNone
	}

	// Evaluate spread
	signal := evaluator.Evaluate(tickA, tickB)

	// Emit spread update event
	openCounter, closeCounter := evaluator.Counters()
	spread := evaluator.CurrentSpread(tickA, tickB)

	if e.onEvent != nil {
		e.onEvent(model.WSEvent{
			Type:   "spread_update",
			TaskID: taskID,
			Data: model.SpreadUpdate{
				Symbol:       task.Symbol,
				ExchangeA:    task.ExchangeA,
				ExchangeB:    task.ExchangeB,
				BidA:         tickA.Bid,
				AskA:         tickA.Ask,
				BidB:         tickB.Bid,
				AskB:         tickB.Ask,
				Spread:       spread,
				OpenCounter:  openCounter,
				CloseCounter: closeCounter,
			},
		})
	}

	// Execute trades based on signal
	if signal == SignalOpen {
		currentPos := e.positions[taskID]
		if currentPos+task.QuantityPerOrder <= task.MaxPositionQty {
			if err := e.executeOpenLocked(ctx, task); err != nil {
				log.Printf("[engine] Failed to execute open for task %s: %v", taskID, err)
				// Stop task on error
				e.taskMgr.SetStatus(taskID, model.StatusStopped)
				if e.onEvent != nil {
					e.onEvent(model.WSEvent{
						Type:   "error",
						TaskID: taskID,
						Data:   map[string]string{"error": err.Error()},
					})
				}
			}
		} else {
			log.Printf("[engine] Task %s: max position reached (%f + %f > %f)",
				taskID, currentPos, task.QuantityPerOrder, task.MaxPositionQty)
		}
	} else if signal == SignalClose {
		if e.positions[taskID] > 0 {
			if err := e.executeCloseLocked(ctx, task); err != nil {
				log.Printf("[engine] Failed to execute close for task %s: %v", taskID, err)
				// Stop task on error
				e.taskMgr.SetStatus(taskID, model.StatusStopped)
				if e.onEvent != nil {
					e.onEvent(model.WSEvent{
						Type:   "error",
						TaskID: taskID,
						Data:   map[string]string{"error": err.Error()},
					})
				}
			}
		}
	}

	return signal
}

// StartTask initializes and starts a trading task
func (e *Engine) StartTask(ctx context.Context, taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return err
	}

	// Set status to running
	if err := e.taskMgr.SetStatus(taskID, model.StatusRunning); err != nil {
		return err
	}

	// Create evaluator
	e.evaluators[taskID] = NewSpreadEvaluator(task)

	// Subscribe to WS for both exchanges (only if wsMgr exists)
	if e.wsMgr != nil {
		if err := e.wsMgr.Subscribe(ctx, task.ExchangeA, task.Symbol); err != nil {
			e.taskMgr.SetStatus(taskID, model.StatusStopped)
			return fmt.Errorf("failed to subscribe to %s: %w", task.ExchangeA, err)
		}
		if err := e.wsMgr.Subscribe(ctx, task.ExchangeB, task.Symbol); err != nil {
			e.taskMgr.SetStatus(taskID, model.StatusStopped)
			return fmt.Errorf("failed to subscribe to %s: %w", task.ExchangeB, err)
		}
	}

	log.Printf("[engine] Started task %s: %s %s/%s", taskID, task.Direction, task.ExchangeA, task.ExchangeB)
	return nil
}

// StopTask stops a running task
func (e *Engine) StopTask(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, err := e.taskMgr.Get(taskID)
	if err != nil {
		return err
	}

	// Set status to stopped
	if err := e.taskMgr.SetStatus(taskID, model.StatusStopped); err != nil {
		return err
	}

	// Remove evaluator and reset counters
	delete(e.evaluators, taskID)

	log.Printf("[engine] Stopped task %s", taskID)
	return nil
}

// ManualOpen executes an open trade regardless of thresholds
func (e *Engine) ManualOpen(ctx context.Context, taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return err
	}

	// Check max position
	currentPos := e.positions[taskID]
	if currentPos+task.QuantityPerOrder > task.MaxPositionQty {
		return fmt.Errorf("max position reached: %f + %f > %f",
			currentPos, task.QuantityPerOrder, task.MaxPositionQty)
	}

	return e.executeOpenLocked(ctx, task)
}

// ManualClose executes a close trade regardless of thresholds
func (e *Engine) ManualClose(ctx context.Context, taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return err
	}

	if e.positions[taskID] <= 0 {
		return fmt.Errorf("no position to close")
	}

	return e.executeCloseLocked(ctx, task)
}

// executeOpenLocked executes an open trade (caller must hold lock)
func (e *Engine) executeOpenLocked(ctx context.Context, task *model.Task) error {
	// Determine sides based on direction
	var sideA, sideB string
	switch task.Direction {
	case model.ShortSpread:
		// Short spread open: SELL on A, BUY on B
		sideA = "SELL"
		sideB = "BUY"
	case model.LongSpread:
		// Long spread open: BUY on A, SELL on B
		sideA = "BUY"
		sideB = "SELL"
	default:
		return fmt.Errorf("unknown direction: %s", task.Direction)
	}

	// Get clients
	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		return fmt.Errorf("client not found")
	}

	// Place both orders concurrently
	var wg sync.WaitGroup
	var orderA, orderB *model.Order
	var errA, errB error

	wg.Add(2)
	go func() {
		defer wg.Done()
		orderA, errA = e.placeOrder(ctx, clientA, task.Symbol, sideA, task.QuantityPerOrder)
	}()
	go func() {
		defer wg.Done()
		orderB, errB = e.placeOrder(ctx, clientB, task.Symbol, sideB, task.QuantityPerOrder)
	}()
	wg.Wait()

	// Check for errors
	if errA != nil && errB != nil {
		return fmt.Errorf("both orders failed: A=%v, B=%v", errA, errB)
	}
	if errA != nil {
		// Try to reverse order B
		if orderB != nil {
			reverseSide := "SELL"
			if sideB == "SELL" {
				reverseSide = "BUY"
			}
			_, _ = e.placeOrder(ctx, clientB, task.Symbol, reverseSide, task.QuantityPerOrder)
		}
		return fmt.Errorf("order A failed: %v", errA)
	}
	if errB != nil {
		// Try to reverse order A
		if orderA != nil {
			reverseSide := "SELL"
			if sideA == "SELL" {
				reverseSide = "BUY"
			}
			_, _ = e.placeOrder(ctx, clientA, task.Symbol, reverseSide, task.QuantityPerOrder)
		}
		return fmt.Errorf("order B failed: %v", errB)
	}

	// Update position
	e.positions[task.ID] += task.QuantityPerOrder

	// Emit trade_executed event
	if e.onEvent != nil {
		e.onEvent(model.WSEvent{
			Type:   "trade_executed",
			TaskID: task.ID,
			Data: map[string]interface{}{
				"action":   "open",
				"order_a":  orderA,
				"order_b":  orderB,
				"position": e.positions[task.ID],
			},
		})
	}

	log.Printf("[engine] Executed open for task %s: %s@%s, %s@%s",
		task.ID, sideA, task.ExchangeA, sideB, task.ExchangeB)
	return nil
}

// executeCloseLocked executes a close trade (caller must hold lock)
func (e *Engine) executeCloseLocked(ctx context.Context, task *model.Task) error {
	// Determine sides (opposite of open)
	var sideA, sideB string
	switch task.Direction {
	case model.ShortSpread:
		// Short spread close: BUY on A, SELL on B
		sideA = "BUY"
		sideB = "SELL"
	case model.LongSpread:
		// Long spread close: SELL on A, BUY on B
		sideA = "SELL"
		sideB = "BUY"
	default:
		return fmt.Errorf("unknown direction: %s", task.Direction)
	}

	// Get clients
	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		return fmt.Errorf("client not found")
	}

	// Place both orders concurrently
	var wg sync.WaitGroup
	var orderA, orderB *model.Order
	var errA, errB error

	wg.Add(2)
	go func() {
		defer wg.Done()
		orderA, errA = e.placeOrder(ctx, clientA, task.Symbol, sideA, task.QuantityPerOrder)
	}()
	go func() {
		defer wg.Done()
		orderB, errB = e.placeOrder(ctx, clientB, task.Symbol, sideB, task.QuantityPerOrder)
	}()
	wg.Wait()

	// Check for errors
	if errA != nil && errB != nil {
		return fmt.Errorf("both orders failed: A=%v, B=%v", errA, errB)
	}
	if errA != nil {
		// Try to reverse order B
		if orderB != nil {
			reverseSide := "SELL"
			if sideB == "SELL" {
				reverseSide = "BUY"
			}
			_, _ = e.placeOrder(ctx, clientB, task.Symbol, reverseSide, task.QuantityPerOrder)
		}
		return fmt.Errorf("order A failed: %v", errA)
	}
	if errB != nil {
		// Try to reverse order A
		if orderA != nil {
			reverseSide := "SELL"
			if sideA == "SELL" {
				reverseSide = "BUY"
			}
			_, _ = e.placeOrder(ctx, clientA, task.Symbol, reverseSide, task.QuantityPerOrder)
		}
		return fmt.Errorf("order B failed: %v", errB)
	}

	// Update position (min 0)
	e.positions[task.ID] -= task.QuantityPerOrder
	if e.positions[task.ID] < 0 {
		e.positions[task.ID] = 0
	}

	// Emit trade_executed event
	if e.onEvent != nil {
		e.onEvent(model.WSEvent{
			Type:   "trade_executed",
			TaskID: task.ID,
			Data: map[string]interface{}{
				"action":   "close",
				"order_a":  orderA,
				"order_b":  orderB,
				"position": e.positions[task.ID],
			},
		})
	}

	log.Printf("[engine] Executed close for task %s: %s@%s, %s@%s",
		task.ID, sideA, task.ExchangeA, sideB, task.ExchangeB)
	return nil
}

// placeOrder is a helper that handles both real clients and test mocks
func (e *Engine) placeOrder(ctx context.Context, client interface{}, symbol, side string, qty float64) (*model.Order, error) {
	// Try as real exchange.Client
	if c, ok := client.(exchange.Client); ok {
		return c.PlaceMarketOrder(ctx, symbol, side, qty)
	}

	// Try as test mock (has PlaceMarketOrder method)
	type orderPlacer interface {
		PlaceMarketOrder(ctx context.Context, symbol, side string, qty float64) (*model.Order, error)
	}
	if c, ok := client.(orderPlacer); ok {
		return c.PlaceMarketOrder(ctx, symbol, side, qty)
	}

	return nil, fmt.Errorf("invalid client type")
}

// GetPositionQty returns the current position quantity for a task
func (e *Engine) GetPositionQty(taskID string) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.positions[taskID]
}

// SetPositionQty sets the position quantity for a task (for testing)
func (e *Engine) SetPositionQty(taskID string, qty float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.positions[taskID] = qty
}
