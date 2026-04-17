package engine

import (
	"time"
	"spread-arbitrage/internal/model"
)

type Signal int

const (
	SignalNone  Signal = iota
	SignalOpen
	SignalClose
)

type ConfirmCounter struct {
	required int
	current  int
}

func NewConfirmCounter(required int) *ConfirmCounter {
	return &ConfirmCounter{required: required}
}

func (c *ConfirmCounter) Update(conditionMet bool) bool {
	if !conditionMet {
		c.current = 0
		return false
	}
	c.current++
	if c.current >= c.required {
		c.current = 0 // reset after trigger
		return true
	}
	return false
}

func (c *ConfirmCounter) Current() int { return c.current }
func (c *ConfirmCounter) Reset()       { c.current = 0 }

type SpreadEvaluator struct {
	task         *model.Task
	openCounter  *ConfirmCounter
	closeCounter *ConfirmCounter
}

func NewSpreadEvaluator(task *model.Task) *SpreadEvaluator {
	return &SpreadEvaluator{
		task:         task,
		openCounter:  NewConfirmCounter(task.ConfirmCount),
		closeCounter: NewConfirmCounter(task.ConfirmCount),
	}
}

// Evaluate checks the spread between two tickers and returns a signal
func (se *SpreadEvaluator) Evaluate(a, b model.BookTicker) Signal {
	maxLatency := time.Duration(se.task.DataMaxLatencyMs) * time.Millisecond

	// Latency check 1: Exchange timestamp vs receive time
	if a.ReceivedAt.Sub(a.Timestamp) >= maxLatency {
		se.openCounter.Reset()
		se.closeCounter.Reset()
		return SignalNone
	}
	if b.ReceivedAt.Sub(b.Timestamp) >= maxLatency {
		se.openCounter.Reset()
		se.closeCounter.Reset()
		return SignalNone
	}

	// Latency check 2: Freshness check
	now := time.Now()
	if now.Sub(a.ReceivedAt) >= maxLatency {
		se.openCounter.Reset()
		se.closeCounter.Reset()
		return SignalNone
	}
	if now.Sub(b.ReceivedAt) >= maxLatency {
		se.openCounter.Reset()
		se.closeCounter.Reset()
		return SignalNone
	}

	// Calculate spread based on direction
	var openCondition, closeCondition bool

	switch se.task.Direction {
	case model.ShortSpread:
		// Short spread: sell A (at bid), buy B (at ask)
		// Open when A.bid - B.ask > threshold (positive spread)
		// Close when A.ask - B.bid < threshold (spread narrowed)
		openCondition = a.Bid - b.Ask > se.task.OpenThreshold
		closeCondition = a.Ask - b.Bid < se.task.CloseThreshold

	case model.LongSpread:
		// Long spread: buy A (at ask), sell B (at bid)
		// Open when A.ask - B.bid < threshold (negative spread)
		// Close when A.bid - B.ask > threshold (spread narrowed)
		openCondition = a.Ask - b.Bid < se.task.OpenThreshold
		closeCondition = a.Bid - b.Ask > se.task.CloseThreshold
	}

	// Update counters and check for signals
	if se.openCounter.Update(openCondition) {
		return SignalOpen
	}
	if se.closeCounter.Update(closeCondition) {
		return SignalClose
	}

	// If neither condition met, both counters reset automatically
	return SignalNone
}

// CurrentSpread returns the current spread value for display (A.bid - B.ask)
func (se *SpreadEvaluator) CurrentSpread(a, b model.BookTicker) float64 {
	return a.Bid - b.Ask
}

// Counters returns the current counter values (open, close)
func (se *SpreadEvaluator) Counters() (int, int) {
	return se.openCounter.Current(), se.closeCounter.Current()
}

// Reset resets both confirm counters. Called after a successful trade
// to prevent queued ticks from immediately triggering another trade.
func (se *SpreadEvaluator) Reset() {
	se.openCounter.Reset()
	se.closeCounter.Reset()
}
