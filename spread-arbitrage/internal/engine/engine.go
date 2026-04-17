package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"spread-arbitrage/internal/clocksync"
	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
	"spread-arbitrage/internal/wsmanager"
)

type EventCallback func(event model.WSEvent)

// tradeLog writes key trading events to both stdout and a daily log file.
var tradeLog *log.Logger
var tradeLogFile *os.File

func initTradeLog() {
	os.MkdirAll("logs", 0755)
	path := filepath.Join("logs", fmt.Sprintf("trades-%s.log", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Failed to open trade log %s: %v", path, err)
		tradeLog = log.New(os.Stdout, "", log.LstdFlags)
		return
	}
	tradeLogFile = f
	tradeLog = log.New(io.MultiWriter(os.Stdout, f), "", log.LstdFlags)
}

func init() {
	initTradeLog()
}

type Engine struct {
	taskMgr    *TaskManager
	wsMgr      *wsmanager.Manager
	clients    map[string]interface{}
	clk        *clocksync.Syncer
	evaluators map[string]*SpreadEvaluator
	positions  map[string]float64 // task ID → cached position qty (synced from exchange)
	mu         sync.RWMutex
	onEvent    EventCallback
	syncStop   chan struct{}
}

func NewEngine(taskMgr *TaskManager, wsMgr *wsmanager.Manager, clients map[string]interface{}, clk *clocksync.Syncer, onEvent EventCallback) *Engine {
	return &Engine{
		taskMgr:    taskMgr,
		wsMgr:      wsMgr,
		clients:    clients,
		clk:        clk,
		evaluators: make(map[string]*SpreadEvaluator),
		positions:  make(map[string]float64),
		onEvent:    onEvent,
		syncStop:   make(chan struct{}),
	}
}

// StartPositionSync starts a background goroutine that syncs position cache
// from exchanges every interval. Call this once at startup.
func (e *Engine) StartPositionSync(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.syncAllPositions()
			case <-e.syncStop:
				return
			}
		}
	}()
}

// StopPositionSync stops the background sync goroutine.
func (e *Engine) StopPositionSync() {
	close(e.syncStop)
}

// syncAllPositions queries exchange positions for all running tasks and updates cache.
func (e *Engine) syncAllPositions() {
	e.mu.RLock()
	tasks := e.taskMgr.List()
	e.mu.RUnlock()

	ctx := context.Background()
	for _, task := range tasks {
		if task.Status != model.StatusRunning {
			continue
		}
		qty, err := e.queryPositionFromExchange(ctx, task)
		if err != nil {
			log.Printf("[engine] SYNC POSITION FAILED task=%s %s %s/%s: %v", task.ID, task.Symbol, task.ExchangeA, task.ExchangeB, err)
			continue
		}
		e.mu.Lock()
		oldQty := e.positions[task.ID]
		e.positions[task.ID] = qty
		e.mu.Unlock()
		if oldQty != qty {
			log.Printf("[engine] SYNC POSITION CHANGED task=%s %s: %.4f → %.4f", task.ID, task.Symbol, oldQty, qty)
		}
	}
}

// queryPositionFromExchange queries real position from both exchanges.
// Returns min(sizeA, sizeB) as the hedged position quantity.
func (e *Engine) queryPositionFromExchange(ctx context.Context, task *model.Task) (float64, error) {
	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		return 0, fmt.Errorf("client not found")
	}

	posA, errA := e.getPosition(ctx, clientA, task.Symbol)
	posB, errB := e.getPosition(ctx, clientB, task.Symbol)
	if errA != nil {
		return 0, fmt.Errorf("query position A: %w", errA)
	}
	if errB != nil {
		return 0, fmt.Errorf("query position B: %w", errB)
	}

	return math.Min(posA.Size, posB.Size), nil
}

// refreshPositionCache is no longer called after trades.
// Position cache is updated optimistically after each trade, and
// synced by the background goroutine (syncAllPositions every 10s).
// Calling it immediately after trade caused a race: exchange hasn't
// settled yet → returns stale value → overwrites optimistic update → over-opens.

// GetPositionQty returns the position quantity for a task.
// Reads from cache first; if no cache entry exists, queries exchange once.
func (e *Engine) GetPositionQty(taskID string) float64 {
	e.mu.RLock()
	qty, exists := e.positions[taskID]
	e.mu.RUnlock()
	if exists {
		return qty
	}

	// Cache miss — try to load from exchange
	e.mu.Lock()
	defer e.mu.Unlock()
	// Double-check after acquiring write lock
	if qty, exists := e.positions[taskID]; exists {
		return qty
	}
	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return 0
	}
	ctx := context.Background()
	exchangeQty, err := e.queryPositionFromExchange(ctx, task)
	if err != nil {
		return 0
	}
	e.positions[taskID] = exchangeQty
	return exchangeQty
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
		if task.Symbol != symbol {
			continue
		}
		if task.ExchangeA != exchangeName && task.ExchangeB != exchangeName {
			continue
		}

		if e.wsMgr == nil {
			continue
		}

		tickA, okA := e.wsMgr.GetLatest(task.ExchangeA, task.Symbol)
		tickB, okB := e.wsMgr.GetLatest(task.ExchangeB, task.Symbol)
		if !okA || !okB {
			continue
		}

		e.ProcessTick(ctx, task.ID, tickA, tickB)
	}
}

// ProcessTick evaluates tickers and executes trades if needed.
// Uses cached position for zero-latency decisions.
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

	// Calculate clock-synced real ages for latency checks and logging
	var durA, durB time.Duration
	if e.clk != nil {
		durA = e.clk.RealAge(task.ExchangeA, tickA.Timestamp)
		durB = e.clk.RealAge(task.ExchangeB, tickB.Timestamp)
	} else {
		now := time.Now()
		durA = now.Sub(tickA.ReceivedAt)
		durB = now.Sub(tickB.ReceivedAt)
	}
	ageA := durA.Milliseconds()
	ageB := durB.Milliseconds()

	prevOpen, prevClose := evaluator.Counters()
	signal := evaluator.Evaluate(tickA, tickB, durA, durB)

	openCounter, closeCounter := evaluator.Counters()
	spread := evaluator.CurrentSpread(tickA, tickB)

	// Log confirm counter changes for debugging
	// When signal triggers, counter resets to 0 internally, so log N/N for that case
	if signal == SignalOpen {
		tradeLog.Printf("[engine] CONFIRM OPEN %d/%d task=%s spread=%.6f threshold=%.6f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
			task.ConfirmCount, task.ConfirmCount, taskID, spread, task.OpenThreshold,
			tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
			tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)
	} else if signal == SignalClose {
		tradeLog.Printf("[engine] CONFIRM CLOSE %d/%d task=%s spread=%.6f threshold=%.6f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
			task.ConfirmCount, task.ConfirmCount, taskID, spread, task.CloseThreshold,
			tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
			tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)
	} else {
		if openCounter > 0 && openCounter > prevOpen {
			tradeLog.Printf("[engine] CONFIRM OPEN %d/%d task=%s spread=%.6f threshold=%.6f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
				openCounter, task.ConfirmCount, taskID, spread, task.OpenThreshold,
				tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
				tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)
		}
		if closeCounter > 0 && closeCounter > prevClose {
			tradeLog.Printf("[engine] CONFIRM CLOSE %d/%d task=%s spread=%.6f threshold=%.6f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
				closeCounter, task.ConfirmCount, taskID, spread, task.CloseThreshold,
				tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
				tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)
		}
	}

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

	// Use cached position for instant decisions
	currentPos := e.positions[taskID]

	if signal == SignalOpen {
		if currentPos+task.QuantityPerOrder <= task.MaxPositionQty {
			tradeLog.Printf("[engine] SIGNAL OPEN task=%s dir=%s spread=%.6f threshold=%.6f pos=%.4f qty=%.4f max=%.4f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
				taskID, task.Direction, spread, task.OpenThreshold, currentPos, task.QuantityPerOrder, task.MaxPositionQty,
				tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
				tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)
			if err := e.executeOpenLocked(ctx, task); err != nil {
				tradeLog.Printf("[engine] OPEN FAILED task=%s: %v", taskID, err)
				e.taskMgr.SetStatus(taskID, model.StatusStopped)
				delete(e.evaluators, taskID)
			} else {
				evaluator.Reset()
			}
		} else {
			tradeLog.Printf("[engine] OPEN BLOCKED (max position) task=%s pos=%.4f + qty=%.4f > max=%.4f",
				taskID, currentPos, task.QuantityPerOrder, task.MaxPositionQty)
			evaluator.Reset()
		}
	} else if signal == SignalClose {
		if currentPos > 0 {
			closeQty := math.Min(task.QuantityPerOrder, currentPos)
			tradeLog.Printf("[engine] SIGNAL CLOSE task=%s dir=%s spread=%.6f threshold=%.6f pos=%.4f closeQty=%.4f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
				taskID, task.Direction, spread, task.CloseThreshold, currentPos, closeQty,
				tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
				tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)
			if err := e.executeCloseLocked(ctx, task, closeQty); err != nil {
				tradeLog.Printf("[engine] CLOSE FAILED task=%s: %v", taskID, err)
				e.taskMgr.SetStatus(taskID, model.StatusStopped)
				delete(e.evaluators, taskID)
			} else {
				evaluator.Reset()
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

	if err := e.taskMgr.SetStatus(taskID, model.StatusRunning); err != nil {
		return err
	}

	e.evaluators[taskID] = NewSpreadEvaluator(task)

	// Sync position from exchange on start
	qty, err := e.queryPositionFromExchange(ctx, task)
	if err != nil {
		log.Printf("[engine] Warning: could not query initial position for task %s: %v", taskID, err)
		// Don't fail — start with 0 cache, background sync will catch up
	} else {
		e.positions[taskID] = qty
		log.Printf("[engine] Task %s initial position: %.4f", taskID, qty)
	}

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

	if err := e.taskMgr.SetStatus(taskID, model.StatusStopped); err != nil {
		return err
	}

	delete(e.evaluators, taskID)

	log.Printf("[engine] Stopped task %s", taskID)
	return nil
}

// ensurePositionCached queries exchange if the cache has no entry for this task.
func (e *Engine) ensurePositionCached(ctx context.Context, task *model.Task) {
	if _, exists := e.positions[task.ID]; !exists {
		qty, err := e.queryPositionFromExchange(ctx, task)
		if err != nil {
			log.Printf("[engine] Failed to query position for task %s: %v", task.ID, err)
			return
		}
		e.positions[task.ID] = qty
		log.Printf("[engine] Loaded position from exchange for task %s: %.4f", task.ID, qty)
	}
}

// ManualOpen executes an open trade regardless of thresholds
func (e *Engine) ManualOpen(ctx context.Context, taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return err
	}

	e.ensurePositionCached(ctx, task)

	currentPos := e.positions[taskID]
	if currentPos+task.QuantityPerOrder > task.MaxPositionQty {
		return fmt.Errorf("max position reached: %.4f + %.4f > %.4f",
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

	e.ensurePositionCached(ctx, task)

	currentPos := e.positions[taskID]
	if currentPos <= 0 {
		return fmt.Errorf("no position to close (exchange position: %.4f)", currentPos)
	}

	closeQty := math.Min(task.QuantityPerOrder, currentPos)
	return e.executeCloseLocked(ctx, task, closeQty)
}

// executeOpenLocked executes an open trade (caller must hold lock)
func (e *Engine) executeOpenLocked(ctx context.Context, task *model.Task) error {
	var sideA, sideB string
	switch task.Direction {
	case model.ShortSpread:
		sideA = "SELL"
		sideB = "BUY"
	case model.LongSpread:
		sideA = "BUY"
		sideB = "SELL"
	default:
		return fmt.Errorf("unknown direction: %s", task.Direction)
	}

	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		return fmt.Errorf("client not found")
	}

	var wg sync.WaitGroup
	var orderA, orderB *model.Order
	var errA, errB error

	triggerID := genTriggerID()
	cidA := fmt.Sprintf("sa-%s-A", triggerID)
	cidB := fmt.Sprintf("sa-%s-B", triggerID)

	wg.Add(2)
	go func() {
		defer wg.Done()
		orderA, errA = e.placeOrder(ctx, clientA, task.Symbol, sideA, task.QuantityPerOrder, cidA)
	}()
	go func() {
		defer wg.Done()
		orderB, errB = e.placeOrder(ctx, clientB, task.Symbol, sideB, task.QuantityPerOrder, cidB)
	}()
	wg.Wait()

	if errA != nil && errB != nil {
		return fmt.Errorf("both orders failed: A=%v, B=%v", errA, errB)
	}

	if errA != nil || errB != nil {
		e.handleSingleLegFailure(ctx, task, "open", errA, errB, clientA, clientB, sideA, sideB, task.QuantityPerOrder)
		failedSide := "A"
		failedErr := errA
		if errB != nil {
			failedSide = "B"
			failedErr = errB
		}
		return fmt.Errorf("order %s failed: %v", failedSide, failedErr)
	}

	// Both succeeded — update cache optimistically, then refresh from exchange
	e.positions[task.ID] += task.QuantityPerOrder

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

	// Position cache updated optimistically above; background sync will reconcile

	tradeLog.Printf("[engine] Executed open for task %s: %s@%s, %s@%s (position: %.4f)",
		task.ID, sideA, task.ExchangeA, sideB, task.ExchangeB, e.positions[task.ID])
	return nil
}

// executeCloseLocked executes a close trade (caller must hold lock).
// qty is the actual amount to close — must be min(QuantityPerOrder, currentPosition).
func (e *Engine) executeCloseLocked(ctx context.Context, task *model.Task, qty float64) error {
	var sideA, sideB string
	switch task.Direction {
	case model.ShortSpread:
		sideA = "BUY"
		sideB = "SELL"
	case model.LongSpread:
		sideA = "SELL"
		sideB = "BUY"
	default:
		return fmt.Errorf("unknown direction: %s", task.Direction)
	}

	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		return fmt.Errorf("client not found")
	}

	var wg sync.WaitGroup
	var orderA, orderB *model.Order
	var errA, errB error

	triggerID := genTriggerID()
	cidA := fmt.Sprintf("sa-%s-A", triggerID)
	cidB := fmt.Sprintf("sa-%s-B", triggerID)

	wg.Add(2)
	go func() {
		defer wg.Done()
		orderA, errA = e.placeOrder(ctx, clientA, task.Symbol, sideA, qty, cidA)
	}()
	go func() {
		defer wg.Done()
		orderB, errB = e.placeOrder(ctx, clientB, task.Symbol, sideB, qty, cidB)
	}()
	wg.Wait()

	if errA != nil && errB != nil {
		return fmt.Errorf("both close orders failed: A=%v, B=%v", errA, errB)
	}

	if errA != nil || errB != nil {
		e.handleSingleLegFailure(ctx, task, "close", errA, errB, clientA, clientB, sideA, sideB, qty)
		failedSide := "A"
		failedErr := errA
		if errB != nil {
			failedSide = "B"
			failedErr = errB
		}
		return fmt.Errorf("close order %s failed: %v", failedSide, failedErr)
	}

	// Both succeeded — update cache optimistically, then refresh from exchange
	e.positions[task.ID] -= qty
	if e.positions[task.ID] < 0 {
		e.positions[task.ID] = 0
	}

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

	// Position cache updated optimistically above; background sync will reconcile

	tradeLog.Printf("[engine] Executed close for task %s: %s@%s, %s@%s (position: %.4f)",
		task.ID, sideA, task.ExchangeA, sideB, task.ExchangeB, e.positions[task.ID])
	return nil
}

// handleSingleLegFailure handles the case where one order succeeded and the other failed.
func (e *Engine) handleSingleLegFailure(ctx context.Context, task *model.Task, action string,
	errA, errB error, clientA, clientB interface{}, sideA, sideB string, qty float64) {

	failedSide := "A"
	successSide := "B"
	successExchange := task.ExchangeB
	successClient := clientB
	successOrderSide := sideB
	failedErr := errA
	if errB != nil {
		failedSide = "B"
		successSide = "A"
		successExchange = task.ExchangeA
		successClient = clientA
		successOrderSide = sideA
		failedErr = errB
	}

	reverseSide := "SELL"
	if successOrderSide == "SELL" {
		reverseSide = "BUY"
	}

	var reverseErr error
	for attempt := 1; attempt <= 3; attempt++ {
		tradeLog.Printf("[engine] SINGLE-LEG RISK (%s) task %s: %s failed, reversing %s on %s (attempt %d/3)",
			action, task.ID, failedSide, reverseSide, successExchange, attempt)
		reverseID := fmt.Sprintf("sa-%s-R%d", genTriggerID(), attempt)
		_, reverseErr = e.placeOrder(ctx, successClient, task.Symbol, reverseSide, qty, reverseID)
		if reverseErr == nil {
			tradeLog.Printf("[engine] Reverse succeeded for task %s on %s", task.ID, successExchange)
			break
		}
		tradeLog.Printf("[engine] Reverse attempt %d failed for task %s: %v", attempt, task.ID, reverseErr)
	}

	if reverseErr != nil {
		tradeLog.Printf("[engine] CRITICAL: task %s has NAKED POSITION on %s (%s %.4f %s)",
			task.ID, successExchange, successOrderSide, qty, task.Symbol)
		if e.onEvent != nil {
			e.onEvent(model.WSEvent{
				Type:   "error",
				TaskID: task.ID,
				Data: map[string]interface{}{
					"severity":          "critical",
					"error":             fmt.Sprintf("%s单边风险！%s 下单失败，%s 上的反向操作也失败（重试3次）", action, failedSide, successExchange),
					"naked_exchange":    successExchange,
					"naked_side":        successOrderSide,
					"naked_quantity":    qty,
					"naked_symbol":      task.Symbol,
					"failed_side_error": failedErr.Error(),
					"reverse_error":     reverseErr.Error(),
				},
			})
		}
	} else {
		if e.onEvent != nil {
			e.onEvent(model.WSEvent{
				Type:   "error",
				TaskID: task.ID,
				Data: map[string]interface{}{
					"severity":          "warning",
					"error":             fmt.Sprintf("%s单边失败（%s），已成功反向恢复 %s，任务已停止", action, failedSide, successSide),
					"failed_side_error": failedErr.Error(),
				},
			})
		}
	}

	// Background sync will reconcile position after single-leg event
}

// genTriggerID generates an 8-char hex trigger ID for linking orders
func genTriggerID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// placeOrder handles both real clients and test mocks
func (e *Engine) placeOrder(ctx context.Context, client interface{}, symbol, side string, qty float64, clientOrderID string) (*model.Order, error) {
	if c, ok := client.(exchange.Client); ok {
		return c.PlaceMarketOrder(ctx, symbol, side, qty, clientOrderID)
	}
	type orderPlacer interface {
		PlaceMarketOrder(ctx context.Context, symbol, side string, qty float64, clientOrderID string) (*model.Order, error)
	}
	if c, ok := client.(orderPlacer); ok {
		return c.PlaceMarketOrder(ctx, symbol, side, qty, clientOrderID)
	}
	return nil, fmt.Errorf("invalid client type")
}

// getPosition handles both real clients and test mocks
func (e *Engine) getPosition(ctx context.Context, client interface{}, symbol string) (*model.Position, error) {
	if c, ok := client.(exchange.Client); ok {
		return c.GetPosition(ctx, symbol)
	}
	type positionGetter interface {
		GetPosition(ctx context.Context, symbol string) (*model.Position, error)
	}
	if c, ok := client.(positionGetter); ok {
		return c.GetPosition(ctx, symbol)
	}
	return &model.Position{}, nil
}

// SetPositionQty sets the cached position for testing
func (e *Engine) SetPositionQty(taskID string, qty float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.positions[taskID] = qty
}
