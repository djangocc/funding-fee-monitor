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
	"strings"
	"sync"
	"time"

	"spread-arbitrage/internal/clocksync"
	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
	"spread-arbitrage/internal/wsmanager"
)

type EventCallback func(event model.WSEvent)

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

// posEntry stores position with timestamp for stale-event protection.
type posEntry struct {
	Size      float64
	Timestamp time.Time
}

type Engine struct {
	taskMgr    *TaskManager
	wsMgr      *wsmanager.Manager
	clients    map[string]interface{}
	clk        *clocksync.Syncer
	evaluators map[string]*SpreadEvaluator
	mu         sync.RWMutex

	// Position cache: "exchange:symbol" → {size, timestamp}
	posMu    sync.RWMutex
	posCache map[string]posEntry

	inflight *InflightSet
	balancer *BalanceChecker
	onEvent  EventCallback
	stop     chan struct{}
}

func NewEngine(taskMgr *TaskManager, wsMgr *wsmanager.Manager, clients map[string]interface{}, clk *clocksync.Syncer, onEvent EventCallback) *Engine {
	e := &Engine{
		taskMgr:    taskMgr,
		wsMgr:      wsMgr,
		clients:    clients,
		clk:        clk,
		evaluators: make(map[string]*SpreadEvaluator),
		posCache:   make(map[string]posEntry),
		inflight:   NewInflightSet(),
		onEvent:    onEvent,
		stop:       make(chan struct{}),
	}
	e.balancer = NewBalanceChecker(
		3, // consecutive confirmations
		e.inflight,
		e.getPosFromCache,
		e.placeRebalanceOrder,
		func() []*model.Task { return e.taskMgr.List() },
	)
	e.balancer.onRebalance = func(task *model.Task, exchange, side string, qty, posA, posB float64) {
		if e.onEvent != nil {
			e.onEvent(model.WSEvent{
				Type:   "rebalance",
				TaskID: task.ID,
				Data: map[string]interface{}{
					"exchange": exchange,
					"symbol":   task.Symbol,
					"side":     side,
					"quantity": qty,
					"pos_a":    posA,
					"pos_b":    posB,
				},
			})
		}
	}
	return e
}

// --- Position Cache ---

func posKey(exchangeName, symbol string) string {
	return exchangeName + ":" + symbol
}

func (e *Engine) getPosFromCache(exchangeName, symbol string) float64 {
	e.posMu.RLock()
	defer e.posMu.RUnlock()
	return e.posCache[posKey(exchangeName, symbol)].Size
}

func (e *Engine) updatePosCache(exchangeName, symbol string, size float64, ts time.Time) {
	key := posKey(exchangeName, symbol)
	e.posMu.Lock()
	if existing, ok := e.posCache[key]; ok && ts.Before(existing.Timestamp) {
		e.posMu.Unlock()
		return
	}
	e.posCache[key] = posEntry{Size: size, Timestamp: ts}
	e.posMu.Unlock()
}

// GetHedgedPosition returns min(posA, posB) for a task from the position cache.
func (e *Engine) GetHedgedPosition(taskID string) float64 {
	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return 0
	}
	e.posMu.RLock()
	posA := e.posCache[posKey(task.ExchangeA, task.Symbol)].Size
	posB := e.posCache[posKey(task.ExchangeB, task.Symbol)].Size
	e.posMu.RUnlock()
	return math.Min(posA, posB)
}

// GetPositionQty returns hedged position for backward compatibility (API layer).
func (e *Engine) GetPositionQty(taskID string) float64 {
	return e.GetHedgedPosition(taskID)
}

// --- WS Callbacks ---

// OnOrderUpdate is called when an ORDER_TRADE_UPDATE arrives from any exchange.
func (e *Engine) OnOrderUpdate(update model.OrderUpdate) {
	status := LegFilled
	if update.Status == "CANCELED" || update.Status == "EXPIRED" || update.Status == "REJECTED" {
		status = LegFailed
	}
	if update.Status == "NEW" || update.Status == "PARTIALLY_FILLED" {
		return // not terminal, wait for FILLED/CANCELED
	}

	resolved := e.inflight.ResolveLeg(update.ClientOrderID, LegResult{
		Status:    status,
		FilledQty: update.FilledQty,
		AvgPrice:  update.AvgPrice,
	})

	isOurOrder := strings.HasPrefix(update.ClientOrderID, "sa-") ||
		strings.HasPrefix(update.ClientOrderID, "sm-") ||
		strings.HasPrefix(update.ClientOrderID, "sr-")

	tradeLog.Printf("[engine] ORDER UPDATE %s %s %s status=%s filled=%.4f@%.4f cid=%s resolved=%v ours=%v",
		update.Exchange, update.Symbol, update.Side, update.Status,
		update.FilledQty, update.AvgPrice, update.ClientOrderID, resolved, isOurOrder)

	if !isOurOrder && status == LegFilled && e.onEvent != nil {
		e.onEvent(model.WSEvent{
			Type: "external_order",
			Data: map[string]interface{}{
				"exchange":  update.Exchange,
				"symbol":    update.Symbol,
				"side":      update.Side,
				"quantity":  update.FilledQty,
				"price":     update.AvgPrice,
				"client_id": update.ClientOrderID,
			},
		})
	}
}

// OnAccountUpdate is called when an ACCOUNT_UPDATE arrives from any exchange.
func (e *Engine) OnAccountUpdate(update model.AccountUpdate) {
	e.updatePosCache(update.Exchange, update.Symbol, update.Size, update.Timestamp)

	tradeLog.Printf("[engine] ACCOUNT UPDATE %s %s side=%s size=%.4f reason=%s",
		update.Exchange, update.Symbol, update.Side, update.Size, update.Reason)
}

// --- Tick Processing ---

func (e *Engine) OnTick(exchangeName, symbol string, tick model.BookTicker) {
	e.mu.RLock()
	tasks := e.taskMgr.List()
	e.mu.RUnlock()

	ctx := context.Background()

	for _, task := range tasks {
		if task.Status != model.StatusRunning || task.Symbol != symbol {
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

func (e *Engine) ProcessTick(ctx context.Context, taskID string, tickA, tickB model.BookTicker) Signal {
	e.mu.Lock()

	task, err := e.taskMgr.Get(taskID)
	if err != nil || task.Status != model.StatusRunning {
		e.mu.Unlock()
		return SignalNone
	}

	evaluator, exists := e.evaluators[taskID]
	if !exists {
		e.mu.Unlock()
		return SignalNone
	}

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

	// Log confirm counter changes
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
				Symbol: task.Symbol, ExchangeA: task.ExchangeA, ExchangeB: task.ExchangeB,
				BidA: tickA.Bid, AskA: tickA.Ask, BidB: tickB.Bid, AskB: tickB.Ask,
				Spread: spread, OpenCounter: openCounter, CloseCounter: closeCounter,
				AgeA: ageA, AgeB: ageB,
			},
		})
	}

	currentPos := e.GetHedgedPosition(taskID)

	if signal == SignalOpen {
		if !e.inflight.IsStable() {
			tradeLog.Printf("[engine] SIGNAL OPEN DEFERRED (unstable) task=%s spread=%.6f", taskID, spread)
			e.mu.Unlock()
			return signal
		}
		if currentPos+task.QuantityPerOrder > task.MaxPositionQty {
			tradeLog.Printf("[engine] OPEN BLOCKED (max position) task=%s pos=%.4f + qty=%.4f > max=%.4f",
				taskID, currentPos, task.QuantityPerOrder, task.MaxPositionQty)
			evaluator.Reset()
			e.mu.Unlock()
			return signal
		}

		tradeLog.Printf("[engine] SIGNAL OPEN task=%s dir=%s spread=%.6f threshold=%.6f pos=%.4f qty=%.4f max=%.4f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
			taskID, task.Direction, spread, task.OpenThreshold, currentPos, task.QuantityPerOrder, task.MaxPositionQty,
			tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
			tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)

		triggerID := genTriggerID()
		sideA, sideB := openSides(task.Direction)
		cidA := fmt.Sprintf("sa-%s-A", triggerID)
		cidB := fmt.Sprintf("sa-%s-B", triggerID)

		e.inflight.Add(&InflightOrder{
			TriggerID: triggerID, TaskID: taskID,
			Action: "open", Source: "auto",
			ExchangeA: task.ExchangeA, ExchangeB: task.ExchangeB, Symbol: task.Symbol,
			QtyA: task.QuantityPerOrder, QtyB: task.QuantityPerOrder,
			LegA: LegPending, LegB: LegPending,
			CidA: cidA, CidB: cidB, CreatedAt: time.Now(),
		})
		evaluator.Reset()

		taskCopy := *task
		e.mu.Unlock()
		go e.executeTrade(ctx, &taskCopy, triggerID, "open", sideA, sideB, cidA, cidB, task.QuantityPerOrder)
		return signal
	}

	if signal == SignalClose {
		if !e.inflight.IsStable() {
			tradeLog.Printf("[engine] SIGNAL CLOSE DEFERRED (unstable) task=%s spread=%.6f", taskID, spread)
			e.mu.Unlock()
			return signal
		}
		if currentPos <= 0 {
			e.mu.Unlock()
			return signal
		}

		closeQty := math.Min(task.QuantityPerOrder, currentPos)
		tradeLog.Printf("[engine] SIGNAL CLOSE task=%s dir=%s spread=%.6f threshold=%.6f pos=%.4f closeQty=%.4f A=[bid=%.6f ask=%.6f ts=%s age=%dms] B=[bid=%.6f ask=%.6f ts=%s age=%dms]",
			taskID, task.Direction, spread, task.CloseThreshold, currentPos, closeQty,
			tickA.Bid, tickA.Ask, tickA.Timestamp.Format("15:04:05.000"), ageA,
			tickB.Bid, tickB.Ask, tickB.Timestamp.Format("15:04:05.000"), ageB)

		triggerID := genTriggerID()
		sideA, sideB := closeSides(task.Direction)
		cidA := fmt.Sprintf("sa-%s-A", triggerID)
		cidB := fmt.Sprintf("sa-%s-B", triggerID)

		e.inflight.Add(&InflightOrder{
			TriggerID: triggerID, TaskID: taskID,
			Action: "close", Source: "auto",
			ExchangeA: task.ExchangeA, ExchangeB: task.ExchangeB, Symbol: task.Symbol,
			QtyA: closeQty, QtyB: closeQty,
			LegA: LegPending, LegB: LegPending,
			CidA: cidA, CidB: cidB, CreatedAt: time.Now(),
		})
		evaluator.Reset()

		taskCopy := *task
		e.mu.Unlock()
		go e.executeTrade(ctx, &taskCopy, triggerID, "close", sideA, sideB, cidA, cidB, closeQty)
		return signal
	}

	e.mu.Unlock()
	return signal
}

// --- Trade Execution (async, no engine lock held) ---

func (e *Engine) executeTrade(ctx context.Context, task *model.Task, triggerID, action, sideA, sideB, cidA, cidB string, qty float64) {
	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		tradeLog.Printf("[engine] TRADE FAILED task=%s: client not found", task.ID)
		e.inflight.ResolveLeg(cidA, LegResult{Status: LegFailed})
		e.inflight.ResolveLeg(cidB, LegResult{Status: LegFailed})
		return
	}

	var wg sync.WaitGroup
	var orderA, orderB *model.Order
	var errA, errB error

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

	// Handle failures: resolve inflight legs for failed orders immediately.
	// Successful orders will be resolved by WS ORDER_TRADE_UPDATE callback.
	if errA != nil {
		tradeLog.Printf("[engine] ORDER FAILED %s task=%s %s@%s: %v", action, task.ID, sideA, task.ExchangeA, errA)
		e.inflight.ResolveLeg(cidA, LegResult{Status: LegFailed})
	}
	if errB != nil {
		tradeLog.Printf("[engine] ORDER FAILED %s task=%s %s@%s: %v", action, task.ID, sideB, task.ExchangeB, errB)
		e.inflight.ResolveLeg(cidB, LegResult{Status: LegFailed})
	}

	if errA != nil && errB != nil {
		tradeLog.Printf("[engine] BOTH ORDERS FAILED task=%s: A=%v B=%v", task.ID, errA, errB)
		return
	}

	// Single-leg failure: the successful side's order will be resolved by WS callback.
	// BalanceChecker will detect the imbalance in 2s intervals and rebalance.
	if errA != nil || errB != nil {
		failedSide := "A"
		if errB != nil {
			failedSide = "B"
		}
		tradeLog.Printf("[engine] SINGLE-LEG FAILURE (%s) task=%s side=%s — BalanceChecker will handle",
			action, task.ID, failedSide)

		e.mu.Lock()
		e.taskMgr.SetStatus(task.ID, model.StatusStopped)
		delete(e.evaluators, task.ID)
		e.mu.Unlock()

		if e.onEvent != nil {
			e.onEvent(model.WSEvent{
				Type:   "error",
				TaskID: task.ID,
				Data: map[string]interface{}{
					"severity": "warning",
					"error":    fmt.Sprintf("%s单边失败（%s），任务已停止，BalanceChecker 将自动恢复", action, failedSide),
				},
			})
		}
		return
	}

	// Both orders accepted (not necessarily filled yet — WS callback will confirm)
	tradeLog.Printf("[engine] ORDERS PLACED task=%s %s: %s@%s + %s@%s triggerID=%s",
		task.ID, action, sideA, task.ExchangeA, sideB, task.ExchangeB, triggerID)

	if e.onEvent != nil {
		e.onEvent(model.WSEvent{
			Type:   "trade_executed",
			TaskID: task.ID,
			Data: map[string]interface{}{
				"action":  action,
				"order_a": orderA,
				"order_b": orderB,
			},
		})
	}
}

// --- Manual Operations ---

func (e *Engine) ManualOpen(ctx context.Context, taskID string) error {
	if !e.inflight.IsStable() {
		return fmt.Errorf("有在途订单，请等待")
	}

	e.mu.Lock()
	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		e.mu.Unlock()
		return err
	}

	currentPos := e.GetHedgedPosition(taskID)
	if currentPos+task.QuantityPerOrder > task.MaxPositionQty {
		e.mu.Unlock()
		return fmt.Errorf("max position reached: %.4f + %.4f > %.4f",
			currentPos, task.QuantityPerOrder, task.MaxPositionQty)
	}

	triggerID := genTriggerID()
	sideA, sideB := openSides(task.Direction)
	cidA := fmt.Sprintf("sm-%s-A", triggerID)
	cidB := fmt.Sprintf("sm-%s-B", triggerID)

	e.inflight.Add(&InflightOrder{
		TriggerID: triggerID, TaskID: taskID,
		Action: "open", Source: "manual",
		ExchangeA: task.ExchangeA, ExchangeB: task.ExchangeB, Symbol: task.Symbol,
		QtyA: task.QuantityPerOrder, QtyB: task.QuantityPerOrder,
		LegA: LegPending, LegB: LegPending,
		CidA: cidA, CidB: cidB, CreatedAt: time.Now(),
	})

	taskCopy := *task
	e.mu.Unlock()

	go e.executeTrade(ctx, &taskCopy, triggerID, "open", sideA, sideB, cidA, cidB, task.QuantityPerOrder)
	return nil
}

func (e *Engine) ManualClose(ctx context.Context, taskID string) error {
	if !e.inflight.IsStable() {
		return fmt.Errorf("有在途订单，请等待")
	}

	e.mu.Lock()
	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		e.mu.Unlock()
		return err
	}

	currentPos := e.GetHedgedPosition(taskID)
	if currentPos <= 0 {
		e.mu.Unlock()
		return fmt.Errorf("no position to close (hedged position: %.4f)", currentPos)
	}

	closeQty := math.Min(task.QuantityPerOrder, currentPos)
	triggerID := genTriggerID()
	sideA, sideB := closeSides(task.Direction)
	cidA := fmt.Sprintf("sm-%s-A", triggerID)
	cidB := fmt.Sprintf("sm-%s-B", triggerID)

	e.inflight.Add(&InflightOrder{
		TriggerID: triggerID, TaskID: taskID,
		Action: "close", Source: "manual",
		ExchangeA: task.ExchangeA, ExchangeB: task.ExchangeB, Symbol: task.Symbol,
		QtyA: closeQty, QtyB: closeQty,
		LegA: LegPending, LegB: LegPending,
		CidA: cidA, CidB: cidB, CreatedAt: time.Now(),
	})

	taskCopy := *task
	e.mu.Unlock()

	go e.executeTrade(ctx, &taskCopy, triggerID, "close", sideA, sideB, cidA, cidB, closeQty)
	return nil
}

// --- Task Management ---

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

	// Load initial positions from REST
	e.loadPositionFromExchange(ctx, task)

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

func (e *Engine) StopTask(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, err := e.taskMgr.Get(taskID); err != nil {
		return err
	}

	if err := e.taskMgr.SetStatus(taskID, model.StatusStopped); err != nil {
		return err
	}

	delete(e.evaluators, taskID)
	log.Printf("[engine] Stopped task %s", taskID)
	return nil
}

// --- Background: Position Sync + Inflight Timeout ---

// StartBackgroundJobs starts position sync and inflight timeout checker.
func (e *Engine) StartBackgroundJobs() {
	// REST position sync fallback (every 5s)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.syncPositionsREST()
			case <-e.stop:
				return
			}
		}
	}()

	// Inflight timeout checker (every 2s, timeout 5s)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.resolveStaleInflight()
			case <-e.stop:
				return
			}
		}
	}()

	// Balance checker (every 2s, 3 confirmations)
	e.balancer.Start(1 * time.Second)
}

func (e *Engine) StopBackgroundJobs() {
	select {
	case <-e.stop:
	default:
		close(e.stop)
	}
	e.balancer.Stop()
}

// Backward-compatible aliases
func (e *Engine) StartPositionSync(_ time.Duration) { e.StartBackgroundJobs() }
func (e *Engine) StopPositionSync()                 { e.StopBackgroundJobs() }

func (e *Engine) syncPositionsREST() {
	tasks := e.taskMgr.List()
	ctx := context.Background()
	for _, task := range tasks {
		if task.Status != model.StatusRunning {
			continue
		}
		e.loadPositionFromExchange(ctx, task)
	}
}

func (e *Engine) loadPositionFromExchange(ctx context.Context, task *model.Task) {
	clientA, okA := e.clients[task.ExchangeA]
	clientB, okB := e.clients[task.ExchangeB]
	if !okA || !okB {
		return
	}

	var posA, posB *model.Position
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		posA, errA = e.getPosition(ctx, clientA, task.Symbol)
	}()
	go func() {
		defer wg.Done()
		posB, errB = e.getPosition(ctx, clientB, task.Symbol)
	}()
	wg.Wait()

	if errA == nil {
		e.updatePosCache(task.ExchangeA, task.Symbol, posA.Size, time.Now())
	}
	if errB == nil {
		e.updatePosCache(task.ExchangeB, task.Symbol, posB.Size, time.Now())
	}

	if errA == nil && errB == nil {
		log.Printf("[engine] SYNC POS %s: %s=%.4f %s=%.4f",
			task.Symbol, task.ExchangeA, posA.Size, task.ExchangeB, posB.Size)
	}
}

func (e *Engine) resolveStaleInflight() {
	stale := e.inflight.PurgeStale(5 * time.Second)
	if len(stale) == 0 {
		return
	}

	ctx := context.Background()
	for _, order := range stale {
		tradeLog.Printf("[engine] INFLIGHT TIMEOUT triggerID=%s task=%s action=%s — querying REST",
			order.TriggerID, order.TaskID, order.Action)

		// Query real positions to reconcile
		task, err := e.taskMgr.Get(order.TaskID)
		if err == nil {
			e.loadPositionFromExchange(ctx, task)
		}
	}
}

// --- Rebalance Order Placement ---

func (e *Engine) placeRebalanceOrder(task *model.Task, exchangeName, side string, qty float64) string {
	triggerID := genTriggerID()
	cidA := fmt.Sprintf("sr-%s-R", triggerID)

	legB := LegFilled // single-leg: only leg A matters
	cidB := ""

	e.inflight.Add(&InflightOrder{
		TriggerID: triggerID, TaskID: task.ID,
		Action: "rebalance", Source: "rebalance",
		ExchangeA: exchangeName, Symbol: task.Symbol,
		QtyA: qty, LegA: LegPending, LegB: legB,
		CidA: cidA, CidB: cidB, CreatedAt: time.Now(),
	})

	client, ok := e.clients[exchangeName]
	if !ok {
		e.inflight.ResolveLeg(cidA, LegResult{Status: LegFailed})
		return triggerID
	}

	go func() {
		ctx := context.Background()
		_, err := e.placeOrder(ctx, client, task.Symbol, side, qty, cidA)
		if err != nil {
			tradeLog.Printf("[engine] REBALANCE FAILED %s %s %.4f on %s: %v",
				side, task.Symbol, qty, exchangeName, err)
			e.inflight.ResolveLeg(cidA, LegResult{Status: LegFailed})
		}
		// Success: WS callback will resolve
	}()

	return triggerID
}

// --- Helpers ---

func openSides(dir model.Direction) (string, string) {
	switch dir {
	case model.ShortSpread:
		return "SELL", "BUY"
	case model.LongSpread:
		return "BUY", "SELL"
	default:
		return "", ""
	}
}

func closeSides(dir model.Direction) (string, string) {
	switch dir {
	case model.ShortSpread:
		return "BUY", "SELL"
	case model.LongSpread:
		return "SELL", "BUY"
	default:
		return "", ""
	}
}

func genTriggerID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (e *Engine) placeOrder(ctx context.Context, client interface{}, symbol, side string, qty float64, clientOrderID string) (*model.Order, error) {
	// Try WS order placement first (lower latency)
	if wsPlacer, ok := client.(exchange.WSOrderPlacer); ok {
		order, err := wsPlacer.PlaceMarketOrderWS(ctx, symbol, side, qty, clientOrderID)
		if err == nil {
			return order, nil
		}
		log.Printf("[engine] WS order failed, falling back to REST: %v", err)
	}

	// REST fallback
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

// SetPositionQty sets cached position for testing (backward compat).
func (e *Engine) SetPositionQty(taskID string, qty float64) {
	task, err := e.taskMgr.Get(taskID)
	if err != nil {
		return
	}
	e.updatePosCache(task.ExchangeA, task.Symbol, qty, time.Now())
	e.updatePosCache(task.ExchangeB, task.Symbol, qty, time.Now())
}
