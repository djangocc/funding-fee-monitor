package engine

import (
	"testing"
	"time"
	"spread-arbitrage/internal/model"
)

// --- ConfirmCounter tests ---

func TestConfirmCounter_TriggersAfterN(t *testing.T) {
	cc := NewConfirmCounter(3)
	if cc.Update(true) { t.Fatal("should not trigger on 1st") }
	if cc.Update(true) { t.Fatal("should not trigger on 2nd") }
	if !cc.Update(true) { t.Fatal("should trigger on 3rd") }
}

func TestConfirmCounter_ResetsOnFalse(t *testing.T) {
	cc := NewConfirmCounter(3)
	cc.Update(true)
	cc.Update(true)
	cc.Update(false) // reset
	if cc.Update(true) { t.Fatal("should not trigger after reset") }
	if cc.Update(true) { t.Fatal("should not trigger, only 2 consecutive") }
}

func TestConfirmCounter_ResetsAfterTrigger(t *testing.T) {
	cc := NewConfirmCounter(2)
	if cc.Update(true) { t.Fatal("1st") }
	if !cc.Update(true) { t.Fatal("should trigger on 2nd") }
	// After trigger, counter resets
	if cc.Update(true) { t.Fatal("should not trigger, just 1 after reset") }
	if !cc.Update(true) { t.Fatal("should trigger again on 2nd") }
}

// --- SpreadEvaluator tests ---

func freshTicker(exchange string, bid, ask float64) model.BookTicker {
	now := time.Now()
	return model.BookTicker{
		Exchange:   exchange,
		Bid:        bid,
		Ask:        ask,
		Timestamp:  now,
		ReceivedAt: now,
	}
}

func staleTicker(exchange string, bid, ask float64) model.BookTicker {
	return model.BookTicker{
		Exchange:   exchange,
		Bid:        bid,
		Ask:        ask,
		Timestamp:  time.Now().Add(-5 * time.Second),
		ReceivedAt: time.Now().Add(-5 * time.Second),
	}
}

func TestSpread_ShortSpread_OpenSignal(t *testing.T) {
	task := &model.Task{
		Direction:      model.ShortSpread,
		OpenThreshold:  0.5,
		CloseThreshold: 0.1,
		ConfirmCount:   1,
		DataMaxLatencyMs: 2000,
	}
	se := NewSpreadEvaluator(task)
	// A.bid=100.5, B.ask=99.8 → spread=0.7 > 0.5 → open
	a := freshTicker("A", 100.5, 100.6)
	b := freshTicker("B", 99.7, 99.8)
	sig := se.Evaluate(a, b)
	if sig != SignalOpen { t.Fatalf("expected SignalOpen, got %v", sig) }
}

func TestSpread_ShortSpread_CloseSignal(t *testing.T) {
	task := &model.Task{
		Direction:      model.ShortSpread,
		OpenThreshold:  0.5,
		CloseThreshold: 0.2,
		ConfirmCount:   1,
		DataMaxLatencyMs: 2000,
	}
	se := NewSpreadEvaluator(task)
	// A.ask=100.1, B.bid=100.0 → spread=0.1 < 0.2 → close
	a := freshTicker("A", 100.0, 100.1)
	b := freshTicker("B", 100.0, 100.1)
	sig := se.Evaluate(a, b)
	if sig != SignalClose { t.Fatalf("expected SignalClose, got %v", sig) }
}

func TestSpread_LongSpread_OpenSignal(t *testing.T) {
	task := &model.Task{
		Direction:      model.LongSpread,
		OpenThreshold:  -0.5,
		CloseThreshold: -0.1,
		ConfirmCount:   1,
		DataMaxLatencyMs: 2000,
	}
	se := NewSpreadEvaluator(task)
	// A.ask=99.8, B.bid=100.5 → spread=-0.7 < -0.5 → open
	a := freshTicker("A", 99.7, 99.8)
	b := freshTicker("B", 100.5, 100.6)
	sig := se.Evaluate(a, b)
	if sig != SignalOpen { t.Fatalf("expected SignalOpen, got %v", sig) }
}

func TestSpread_LongSpread_CloseSignal(t *testing.T) {
	task := &model.Task{
		Direction:      model.LongSpread,
		OpenThreshold:  -0.5,
		CloseThreshold: -0.1,
		ConfirmCount:   1,
		DataMaxLatencyMs: 2000,
	}
	se := NewSpreadEvaluator(task)
	// A.bid=100.0, B.ask=99.9 → spread=0.1 > -0.1 → close
	a := freshTicker("A", 100.0, 100.1)
	b := freshTicker("B", 99.8, 99.9)
	sig := se.Evaluate(a, b)
	if sig != SignalClose { t.Fatalf("expected SignalClose, got %v", sig) }
}

func TestSpread_StaleData_NoSignal(t *testing.T) {
	task := &model.Task{
		Direction:      model.ShortSpread,
		OpenThreshold:  0.5,
		CloseThreshold: 0.1,
		ConfirmCount:   1,
		DataMaxLatencyMs: 1000,
	}
	se := NewSpreadEvaluator(task)
	a := staleTicker("A", 100.5, 100.6)
	b := freshTicker("B", 99.7, 99.8)
	sig := se.Evaluate(a, b)
	if sig != SignalNone { t.Fatalf("expected SignalNone for stale data, got %v", sig) }
}

func TestSpread_NoSignal_BelowThreshold(t *testing.T) {
	task := &model.Task{
		Direction:      model.ShortSpread,
		OpenThreshold:  0.5,
		CloseThreshold: 0.1,
		ConfirmCount:   1,
		DataMaxLatencyMs: 2000,
	}
	se := NewSpreadEvaluator(task)
	// A.bid=100.2, B.ask=100.0 → spread=0.2, between thresholds → no signal
	a := freshTicker("A", 100.2, 100.3)
	b := freshTicker("B", 99.9, 100.0)
	sig := se.Evaluate(a, b)
	if sig != SignalNone { t.Fatalf("expected SignalNone, got %v", sig) }
}

func TestSpread_ConfirmCountIntegration(t *testing.T) {
	task := &model.Task{
		Direction:      model.ShortSpread,
		OpenThreshold:  0.5,
		CloseThreshold: 0.1,
		ConfirmCount:   3,
		DataMaxLatencyMs: 2000,
	}
	se := NewSpreadEvaluator(task)
	a := freshTicker("A", 100.5, 100.6)
	b := freshTicker("B", 99.7, 99.8)
	// Need 3 consecutive to trigger
	if se.Evaluate(a, b) != SignalNone { t.Fatal("should not signal on 1st") }
	if se.Evaluate(a, b) != SignalNone { t.Fatal("should not signal on 2nd") }
	if se.Evaluate(a, b) != SignalOpen { t.Fatal("should signal open on 3rd") }
}
