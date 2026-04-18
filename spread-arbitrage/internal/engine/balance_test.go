package engine

import (
	"sync"
	"testing"
	"time"

	"spread-arbitrage/internal/model"
)

type rebalanceCall struct {
	TaskID   string
	Exchange string
	Side     string
	Qty      float64
}

func setupBalanceTest(positions map[string]float64, tasks []*model.Task) (*BalanceChecker, *InflightSet, *[]rebalanceCall) {
	inflight := NewInflightSet()
	var calls []rebalanceCall
	var mu sync.Mutex

	bc := NewBalanceChecker(
		3, // confirmCount
		inflight,
		func(exchange, symbol string) float64 {
			return positions[exchange+":"+symbol]
		},
		func(task *model.Task, exchange, side string, qty float64) string {
			mu.Lock()
			calls = append(calls, rebalanceCall{TaskID: task.ID, Exchange: exchange, Side: side, Qty: qty})
			mu.Unlock()
			return "rb-trigger"
		},
		func() []*model.Task { return tasks },
	)

	return bc, inflight, &calls
}

func TestBalance_EqualPositions_NoAction(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 4.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)
	bc.check()
	bc.check()
	bc.check()

	if len(*calls) != 0 {
		t.Fatalf("expected no rebalance, got %d", len(*calls))
	}
	if bc.GetCounter("task1") != 0 {
		t.Fatalf("counter should be 0, got %d", bc.GetCounter("task1"))
	}
}

func TestBalance_Imbalance_NeedsConsecutiveConfirmation(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)

	// Check 1: first time seeing this imbalance, count=0 (snapshot set, waiting for stable confirmation)
	bc.check()
	if bc.GetCounter("task1") != 0 {
		t.Fatalf("expected counter 0, got %d", bc.GetCounter("task1"))
	}
	if len(*calls) != 0 {
		t.Fatal("should not rebalance after 1 check")
	}

	// Check 2: same positions, counter=1
	bc.check()
	if bc.GetCounter("task1") != 1 {
		t.Fatalf("expected counter 1, got %d", bc.GetCounter("task1"))
	}

	// Check 3: counter=2
	bc.check()
	if bc.GetCounter("task1") != 2 {
		t.Fatalf("expected counter 2, got %d", bc.GetCounter("task1"))
	}

	// Check 4: counter reaches 3 → rebalance
	bc.check()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 rebalance, got %d", len(*calls))
	}
	if bc.GetCounter("task1") != 0 {
		t.Fatal("counter should reset after rebalance")
	}
}

func TestBalance_ImbalanceResets_OnRecovery(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)

	// Check 1: snapshot set (count=0), Check 2: count=1
	bc.check()
	bc.check()
	if bc.GetCounter("task1") != 1 {
		t.Fatalf("expected 1, got %d", bc.GetCounter("task1"))
	}

	// Position recovers
	positions["aster:BTCUSDT"] = 6.0
	bc.check()
	if bc.GetCounter("task1") != 0 {
		t.Fatal("counter should reset on recovery")
	}
	if len(*calls) != 0 {
		t.Fatal("should not rebalance")
	}
}

func TestBalance_ImbalanceResets_OnPositionChange(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)

	// Check 1: snapshot set (count=0), Check 2+3: count=1,2
	bc.check()
	bc.check()
	bc.check()
	if bc.GetCounter("task1") != 2 {
		t.Fatalf("expected 2, got %d", bc.GetCounter("task1"))
	}

	// Position changes but still imbalanced — counter resets to 0
	positions["binance:BTCUSDT"] = 8.0
	bc.check()
	if bc.GetCounter("task1") != 0 {
		t.Fatalf("expected counter reset to 0 on position change, got %d", bc.GetCounter("task1"))
	}
	if len(*calls) != 0 {
		t.Fatal("should not rebalance after position change")
	}

	// Need 3 more stable checks to reach threshold
	bc.check()
	bc.check()
	bc.check()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 rebalance, got %d", len(*calls))
	}
}

func TestBalance_PosAGreater_RebalanceA_ShortSpread(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)
	bc.check() // snapshot set, count=0
	bc.check() // count=1
	bc.check() // count=2
	bc.check() // count=3 → rebalance

	if len(*calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.Exchange != "binance" || c.Side != "BUY" || c.Qty != 2.0 {
		t.Errorf("expected binance BUY 2.0, got %s %s %.4f", c.Exchange, c.Side, c.Qty)
	}
}

func TestBalance_PosBGreater_RebalanceB_ShortSpread(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 3.0,
		"aster:BTCUSDT":   5.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)
	bc.check()
	bc.check()
	bc.check()
	bc.check()

	if len(*calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.Exchange != "aster" || c.Side != "SELL" || c.Qty != 2.0 {
		t.Errorf("expected aster SELL 2.0, got %s %s %.4f", c.Exchange, c.Side, c.Qty)
	}
}

func TestBalance_LongSpread_RebalanceSides(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.LongSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)
	bc.check()
	bc.check()
	bc.check()
	bc.check()

	c := (*calls)[0]
	if c.Exchange != "binance" || c.Side != "SELL" {
		t.Errorf("expected binance SELL for long_spread, got %s %s", c.Exchange, c.Side)
	}
}

func TestBalance_Unstable_BlocksRebalance(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, inflight, calls := setupBalanceTest(positions, tasks)

	inflight.Add(makeOrder("t1", "task1", "a1", "b1"))

	bc.check() // snapshot set
	bc.check()
	bc.check()
	bc.check() // count=3 but unstable

	if len(*calls) != 0 {
		t.Fatal("should not rebalance when unstable")
	}

	inflight.ResolveLeg("a1", filledResult())
	inflight.ResolveLeg("b1", filledResult())

	bc.check()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 rebalance after stable, got %d", len(*calls))
	}
}

func TestBalance_StoppedTask_Skipped(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusStopped,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)
	bc.check()
	bc.check()
	bc.check()
	bc.check()

	if len(*calls) != 0 {
		t.Fatal("should skip stopped tasks")
	}
}

func TestBalance_Ticker(t *testing.T) {
	positions := map[string]float64{
		"binance:BTCUSDT": 6.0,
		"aster:BTCUSDT":   4.0,
	}
	tasks := []*model.Task{{
		ID: "task1", Symbol: "BTCUSDT", ExchangeA: "binance", ExchangeB: "aster",
		Direction: model.ShortSpread, Status: model.StatusRunning,
	}}

	bc, _, calls := setupBalanceTest(positions, tasks)
	bc.Start(30 * time.Millisecond)
	defer bc.Stop()

	time.Sleep(150 * time.Millisecond)

	if len(*calls) < 1 {
		t.Fatal("expected at least 1 rebalance from ticker")
	}
}

