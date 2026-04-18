package engine

import (
	"sync"
	"time"
)

type LegStatus string

const (
	LegPending LegStatus = "pending"
	LegFilled  LegStatus = "filled"
	LegFailed  LegStatus = "failed"
)

type InflightOrder struct {
	TriggerID string
	TaskID    string
	Action    string // "open" | "close" | "rebalance"
	Source    string // "auto" | "manual" | "rebalance"
	ExchangeA string
	ExchangeB string
	Symbol    string
	QtyA      float64
	QtyB      float64   // 0 for single-leg rebalance
	LegA      LegStatus
	LegB      LegStatus // pre-set to LegFilled for single-leg rebalance
	CidA      string
	CidB      string // empty for single-leg rebalance
	CreatedAt time.Time
}

func (o *InflightOrder) isResolved() bool {
	return o.LegA != LegPending && o.LegB != LegPending
}

type LegResult struct {
	Status    LegStatus
	FilledQty float64
	AvgPrice  float64
}

type InflightSet struct {
	mu     sync.Mutex
	orders map[string]*InflightOrder // triggerID → order
	byCid  map[string]string         // clientOrderId → triggerID
}

func NewInflightSet() *InflightSet {
	return &InflightSet{
		orders: make(map[string]*InflightOrder),
		byCid:  make(map[string]string),
	}
}

func (s *InflightSet) Add(order *InflightOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.orders[order.TriggerID] = order
	if order.CidA != "" {
		s.byCid[order.CidA] = order.TriggerID
	}
	if order.CidB != "" {
		s.byCid[order.CidB] = order.TriggerID
	}
}

// ResolveLeg updates the leg status for the given clientOrderId.
// Returns true if this resolved the entire order (both legs done).
func (s *InflightSet) ResolveLeg(clientOrderId string, result LegResult) bool {
	s.mu.Lock()

	triggerID, ok := s.byCid[clientOrderId]
	if !ok {
		s.mu.Unlock()
		return false
	}

	order, ok := s.orders[triggerID]
	if !ok {
		s.mu.Unlock()
		return false
	}

	if order.CidA == clientOrderId {
		order.LegA = result.Status
		if result.FilledQty > 0 {
			order.QtyA = result.FilledQty
		}
	} else if order.CidB == clientOrderId {
		order.LegB = result.Status
		if result.FilledQty > 0 {
			order.QtyB = result.FilledQty
		}
	}

	orderResolved := order.isResolved()
	if orderResolved {
		delete(s.orders, triggerID)
		delete(s.byCid, order.CidA)
		if order.CidB != "" {
			delete(s.byCid, order.CidB)
		}
	}

	s.mu.Unlock()
	return orderResolved
}

func (s *InflightSet) IsStable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.orders) == 0
}

func (s *InflightSet) HasInflight(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, order := range s.orders {
		if order.TaskID == taskID {
			return true
		}
	}
	return false
}

// Get returns the inflight order for a given triggerID, or nil.
func (s *InflightSet) Get(triggerID string) *InflightOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[triggerID]
	if !ok {
		return nil
	}
	cp := *order
	return &cp
}

// PurgeStale removes entries older than timeout and returns them.
func (s *InflightSet) PurgeStale(timeout time.Duration) []*InflightOrder {
	now := time.Now()
	s.mu.Lock()

	var stale []*InflightOrder
	for triggerID, order := range s.orders {
		if now.Sub(order.CreatedAt) > timeout {
			stale = append(stale, order)
			delete(s.orders, triggerID)
			delete(s.byCid, order.CidA)
			if order.CidB != "" {
				delete(s.byCid, order.CidB)
			}
		}
	}

	s.mu.Unlock()
	return stale
}

// Count returns the number of inflight orders.
func (s *InflightSet) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.orders)
}
