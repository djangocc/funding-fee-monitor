package engine

import (
	"sync"
	"testing"
	"time"
)

func makeOrder(triggerID, taskID, cidA, cidB string) *InflightOrder {
	return &InflightOrder{
		TriggerID: triggerID,
		TaskID:    taskID,
		Action:    "open",
		Source:    "auto",
		ExchangeA: "binance",
		ExchangeB: "aster",
		Symbol:    "BTCUSDT",
		QtyA:      1.0,
		QtyB:      1.0,
		LegA:      LegPending,
		LegB:      LegPending,
		CidA:      cidA,
		CidB:      cidB,
		CreatedAt: time.Now(),
	}
}

func makeSingleLegOrder(triggerID, taskID, cidA string) *InflightOrder {
	return &InflightOrder{
		TriggerID: triggerID,
		TaskID:    taskID,
		Action:    "rebalance",
		Source:    "rebalance",
		ExchangeA: "binance",
		Symbol:    "BTCUSDT",
		QtyA:      1.0,
		QtyB:      0,
		LegA:      LegPending,
		LegB:      LegFilled, // single-leg: B already "done"
		CidA:      cidA,
		CidB:      "",
		CreatedAt: time.Now(),
	}
}

func filledResult() LegResult {
	return LegResult{Status: LegFilled, FilledQty: 1.0, AvgPrice: 100.0}
}

func failedResult() LegResult {
	return LegResult{Status: LegFailed}
}

func TestInflight_AddMakesUnstable(t *testing.T) {
	s := NewInflightSet()
	if !s.IsStable() {
		t.Fatal("new set should be stable")
	}
	s.Add(makeOrder("t1", "task1", "cid-a", "cid-b"))
	if s.IsStable() {
		t.Fatal("should be unstable after Add")
	}
	if s.Count() != 1 {
		t.Fatalf("expected count 1, got %d", s.Count())
	}
}

func TestInflight_ResolveBothLegs(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "cid-a", "cid-b"))

	resolved := s.ResolveLeg("cid-a", filledResult())
	if resolved {
		t.Fatal("should not be fully resolved after one leg")
	}
	if s.IsStable() {
		t.Fatal("should still be unstable")
	}

	resolved = s.ResolveLeg("cid-b", filledResult())
	if !resolved {
		t.Fatal("should be fully resolved after both legs")
	}
	if !s.IsStable() {
		t.Fatal("should be stable after both resolved")
	}
}

func TestInflight_ResolveOneLeg_StillUnstable(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "cid-a", "cid-b"))

	s.ResolveLeg("cid-a", filledResult())
	if s.IsStable() {
		t.Fatal("should still be unstable with one leg pending")
	}
}

func TestInflight_CidReverseLookup(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "alpha", "beta"))

	resolved := s.ResolveLeg("beta", filledResult())
	if resolved {
		t.Fatal("should not be resolved, leg A still pending")
	}

	resolved = s.ResolveLeg("alpha", filledResult())
	if !resolved {
		t.Fatal("should be resolved after both legs via CID lookup")
	}
}

func TestInflight_MultipleConcurrentEntries(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "a1", "b1"))
	s.Add(makeOrder("t2", "task2", "a2", "b2"))

	if s.Count() != 2 {
		t.Fatalf("expected 2, got %d", s.Count())
	}

	s.ResolveLeg("a1", filledResult())
	s.ResolveLeg("b1", filledResult())
	// t1 resolved, t2 still pending
	if s.IsStable() {
		t.Fatal("should still be unstable, t2 pending")
	}
	if s.Count() != 1 {
		t.Fatalf("expected 1 remaining, got %d", s.Count())
	}

	s.ResolveLeg("a2", filledResult())
	s.ResolveLeg("b2", filledResult())
	if !s.IsStable() {
		t.Fatal("should be stable after all resolved")
	}
}

func TestInflight_PurgeStaleRemovesOldOnly(t *testing.T) {
	s := NewInflightSet()
	old := &InflightOrder{
		TriggerID: "old", TaskID: "task1",
		LegA: LegPending, LegB: LegPending,
		CidA: "old-a", CidB: "old-b",
		CreatedAt: time.Now().Add(-10 * time.Second),
	}
	fresh := makeOrder("fresh", "task2", "fresh-a", "fresh-b")

	s.Add(old)
	s.Add(fresh)

	stale := s.PurgeStale(5 * time.Second)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale, got %d", len(stale))
	}
	if stale[0].TriggerID != "old" {
		t.Fatalf("expected old entry, got %s", stale[0].TriggerID)
	}
	if s.Count() != 1 {
		t.Fatalf("expected 1 remaining, got %d", s.Count())
	}
}

func TestInflight_PurgeStaleReturnsStaleList(t *testing.T) {
	s := NewInflightSet()
	for i := 0; i < 3; i++ {
		o := &InflightOrder{
			TriggerID: string(rune('a' + i)), TaskID: "task1",
			LegA: LegPending, LegB: LegPending,
			CidA: string(rune('a'+i)) + "-a", CidB: string(rune('a'+i)) + "-b",
			CreatedAt: time.Now().Add(-10 * time.Second),
		}
		s.Add(o)
	}

	stale := s.PurgeStale(5 * time.Second)
	if len(stale) != 3 {
		t.Fatalf("expected 3 stale, got %d", len(stale))
	}
	if !s.IsStable() {
		t.Fatal("should be stable after purging all")
	}
}

func TestInflight_ConcurrentAddResolve(t *testing.T) {
	s := NewInflightSet()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			triggerID := string(rune(i))
			cidA := triggerID + "-a"
			cidB := triggerID + "-b"
			o := makeOrder(triggerID, "task1", cidA, cidB)
			s.Add(o)
			s.ResolveLeg(cidA, filledResult())
			s.ResolveLeg(cidB, filledResult())
		}(i)
	}
	wg.Wait()

	if !s.IsStable() {
		t.Fatalf("should be stable after all resolved, count=%d", s.Count())
	}
}

func TestInflight_SingleLegRebalance(t *testing.T) {
	s := NewInflightSet()

	s.Add(makeSingleLegOrder("rb1", "task1", "rb-a"))
	if s.IsStable() {
		t.Fatal("should be unstable")
	}

	resolved := s.ResolveLeg("rb-a", filledResult())
	if !resolved {
		t.Fatal("should be fully resolved")
	}
	if !s.IsStable() {
		t.Fatal("should be stable")
	}
}

func TestInflight_HasInflightFiltering(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task-A", "a1", "b1"))
	s.Add(makeOrder("t2", "task-B", "a2", "b2"))

	if !s.HasInflight("task-A") {
		t.Fatal("should have inflight for task-A")
	}
	if !s.HasInflight("task-B") {
		t.Fatal("should have inflight for task-B")
	}
	if s.HasInflight("task-C") {
		t.Fatal("should NOT have inflight for task-C")
	}

	// Resolve task-A
	s.ResolveLeg("a1", filledResult())
	s.ResolveLeg("b1", filledResult())
	if s.HasInflight("task-A") {
		t.Fatal("task-A should be resolved")
	}
	if !s.HasInflight("task-B") {
		t.Fatal("task-B should still be inflight")
	}
}

func TestInflight_ResolveUnknownCid(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "a1", "b1"))

	resolved := s.ResolveLeg("unknown-cid", filledResult())
	if resolved {
		t.Fatal("should return false for unknown CID")
	}
	if s.IsStable() {
		t.Fatal("should still be unstable")
	}
}

func TestInflight_FailedLegResolvesOrder(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "a1", "b1"))

	s.ResolveLeg("a1", filledResult())
	resolved := s.ResolveLeg("b1", failedResult())
	if !resolved {
		t.Fatal("failed leg should still resolve the order")
	}
	if !s.IsStable() {
		t.Fatal("should be stable")
	}
}

func TestInflight_GetReturnsCopy(t *testing.T) {
	s := NewInflightSet()
	s.Add(makeOrder("t1", "task1", "a1", "b1"))

	got := s.Get("t1")
	if got == nil {
		t.Fatal("should return order")
	}
	if got.TriggerID != "t1" {
		t.Fatal("wrong triggerID")
	}

	// Modify returned copy should not affect internal state
	got.LegA = LegFilled
	internal := s.Get("t1")
	if internal.LegA != LegPending {
		t.Fatal("internal state should not be affected by copy modification")
	}

	if s.Get("nonexistent") != nil {
		t.Fatal("should return nil for unknown triggerID")
	}
}
