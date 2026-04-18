package engine

import (
	"math"
	"sync"
	"time"

	"spread-arbitrage/internal/model"
)

type balanceSnapshot struct {
	posA  float64
	posB  float64
	count int
}

type BalanceChecker struct {
	mu           sync.Mutex
	snapshots    map[string]*balanceSnapshot // taskID → last seen state
	confirmCount int
	inflight     *InflightSet
	getPos       func(exchange, symbol string) float64
	placeOrder   func(task *model.Task, exchange, side string, qty float64) string
	getTasks     func() []*model.Task
	onRebalance  func(task *model.Task, exchange, side string, qty, posA, posB float64)
	stop         chan struct{}
}

func NewBalanceChecker(
	confirmCount int,
	inflight *InflightSet,
	getPos func(exchange, symbol string) float64,
	placeOrder func(task *model.Task, exchange, side string, qty float64) string,
	getTasks func() []*model.Task,
) *BalanceChecker {
	return &BalanceChecker{
		snapshots:    make(map[string]*balanceSnapshot),
		confirmCount: confirmCount,
		inflight:     inflight,
		getPos:       getPos,
		placeOrder:   placeOrder,
		getTasks:     getTasks,
		stop:         make(chan struct{}),
	}
}

// Start begins periodic balance checking.
func (bc *BalanceChecker) Start(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				bc.check()
			case <-bc.stop:
				return
			}
		}
	}()
}

// Stop stops the periodic checker.
func (bc *BalanceChecker) Stop() {
	select {
	case <-bc.stop:
	default:
		close(bc.stop)
	}
}

func (bc *BalanceChecker) check() {
	tasks := bc.getTasks()
	for _, task := range tasks {
		if task.Status != model.StatusRunning {
			continue
		}
		bc.checkTask(task)
	}
}

func (bc *BalanceChecker) checkTask(task *model.Task) {
	posA := bc.getPos(task.ExchangeA, task.Symbol)
	posB := bc.getPos(task.ExchangeB, task.Symbol)
	diff := math.Abs(posA - posB)

	bc.mu.Lock()

	snap, exists := bc.snapshots[task.ID]
	if !exists {
		snap = &balanceSnapshot{}
		bc.snapshots[task.ID] = snap
	}

	if diff < 1e-8 {
		snap.count = 0
		snap.posA = posA
		snap.posB = posB
		bc.mu.Unlock()
		return
	}

	// If positions changed since last check, reset counter and wait for stable confirmation
	if posA != snap.posA || posB != snap.posB {
		snap.posA = posA
		snap.posB = posB
		snap.count = 0
		bc.mu.Unlock()
		tradeLog.Printf("[balance] IMBALANCE DETECTED task=%s posA=%.4f posB=%.4f diff=%.4f (positions changed, waiting for stable confirmation)",
			task.ID, posA, posB, diff)
		return
	}

	// Positions stable and still imbalanced
	snap.count++
	count := snap.count
	bc.mu.Unlock()

	if count < bc.confirmCount {
		tradeLog.Printf("[balance] IMBALANCE %d/%d task=%s posA=%.4f posB=%.4f diff=%.4f",
			count, bc.confirmCount, task.ID, posA, posB, diff)
		return
	}

	if !bc.inflight.IsStable() {
		tradeLog.Printf("[balance] IMBALANCE CONFIRMED but unstable, waiting task=%s", task.ID)
		return
	}

	// Confirmed imbalance with stable positions, execute rebalance
	bc.mu.Lock()
	snap.count = 0
	bc.mu.Unlock()

	var rebalanceExchange, side string
	if posA > posB {
		rebalanceExchange = task.ExchangeA
		switch task.Direction {
		case model.ShortSpread:
			side = "BUY"
		case model.LongSpread:
			side = "SELL"
		}
	} else {
		rebalanceExchange = task.ExchangeB
		switch task.Direction {
		case model.ShortSpread:
			side = "SELL"
		case model.LongSpread:
			side = "BUY"
		}
	}

	tradeLog.Printf("[balance] REBALANCE task=%s %s %s %.4f on %s (posA=%.4f posB=%.4f)",
		task.ID, side, task.Symbol, diff, rebalanceExchange, posA, posB)

	if bc.onRebalance != nil {
		bc.onRebalance(task, rebalanceExchange, side, diff, posA, posB)
	}

	bc.placeOrder(task, rebalanceExchange, side, diff)
}

// GetCounter returns the current imbalance counter for a task (for testing).
func (bc *BalanceChecker) GetCounter(taskID string) int {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if snap, ok := bc.snapshots[taskID]; ok {
		return snap.count
	}
	return 0
}
