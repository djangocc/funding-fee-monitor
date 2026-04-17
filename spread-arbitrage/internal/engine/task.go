package engine

import (
	"fmt"
	"sync"

	"spread-arbitrage/internal/model"

	"github.com/google/uuid"
)

type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*model.Task
}

func NewTaskManager() *TaskManager {
	return &TaskManager{tasks: make(map[string]*model.Task)}
}

func (tm *TaskManager) Create(req model.TaskCreateRequest) (*model.Task, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	task := &model.Task{
		ID:               uuid.New().String(),
		Symbol:           req.Symbol,
		ExchangeA:        req.ExchangeA,
		ExchangeB:        req.ExchangeB,
		Direction:        req.Direction,
		Status:           model.StatusStopped,
		OpenThreshold:    req.OpenThreshold,
		CloseThreshold:   req.CloseThreshold,
		ConfirmCount:     req.ConfirmCount,
		QuantityPerOrder: req.QuantityPerOrder,
		MaxPositionQty:   req.MaxPositionQty,
		DataMaxLatencyMs: req.DataMaxLatencyMs,
	}

	tm.mu.Lock()
	// Check for duplicate: same symbol + exchangeA + exchangeB
	for _, existing := range tm.tasks {
		if existing.Symbol == task.Symbol && existing.ExchangeA == task.ExchangeA && existing.ExchangeB == task.ExchangeB {
			tm.mu.Unlock()
			return nil, fmt.Errorf("该币对（%s %s/%s）已存在套利任务，同一币对只能有一个任务", task.Symbol, task.ExchangeA, task.ExchangeB)
		}
	}
	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	return task, nil
}

func (tm *TaskManager) Get(id string) (*model.Task, error) {
	tm.mu.RLock()
	task, exists := tm.tasks[id]
	tm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	return task, nil
}

func (tm *TaskManager) List() []*model.Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tasks := make([]*model.Task, 0, len(tm.tasks))
	for _, task := range tm.tasks {
		tasks = append(tasks, task)
	}

	return tasks
}

func (tm *TaskManager) Update(id string, req model.TaskCreateRequest) (*model.Task, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[id]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	// Update all fields except ID and Status
	task.Symbol = req.Symbol
	task.ExchangeA = req.ExchangeA
	task.ExchangeB = req.ExchangeB
	task.Direction = req.Direction
	task.OpenThreshold = req.OpenThreshold
	task.CloseThreshold = req.CloseThreshold
	task.ConfirmCount = req.ConfirmCount
	task.QuantityPerOrder = req.QuantityPerOrder
	task.MaxPositionQty = req.MaxPositionQty
	task.DataMaxLatencyMs = req.DataMaxLatencyMs

	return task, nil
}

func (tm *TaskManager) Delete(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[id]
	if !exists {
		return fmt.Errorf("task not found: %s", id)
	}

	if task.Status == model.StatusRunning {
		return fmt.Errorf("cannot delete running task: %s", id)
	}

	delete(tm.tasks, id)
	return nil
}

func (tm *TaskManager) SetStatus(id string, status model.TaskStatus) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[id]
	if !exists {
		return fmt.Errorf("task not found: %s", id)
	}

	task.Status = status
	return nil
}

func validateRequest(req model.TaskCreateRequest) error {
	// ExchangeA must differ from ExchangeB
	if req.ExchangeA == req.ExchangeB {
		return fmt.Errorf("ExchangeA and ExchangeB must be different")
	}

	// Validate thresholds based on direction
	if req.Direction == model.ShortSpread {
		// For short_spread: OpenThreshold must be > CloseThreshold
		if req.OpenThreshold <= req.CloseThreshold {
			return fmt.Errorf("short_spread requires OpenThreshold > CloseThreshold")
		}
	} else if req.Direction == model.LongSpread {
		// For long_spread: OpenThreshold must be < CloseThreshold
		if req.OpenThreshold >= req.CloseThreshold {
			return fmt.Errorf("long_spread requires OpenThreshold < CloseThreshold")
		}
	}

	// ConfirmCount must be >= 1
	if req.ConfirmCount < 1 {
		return fmt.Errorf("ConfirmCount must be >= 1")
	}

	// QuantityPerOrder must be > 0
	if req.QuantityPerOrder <= 0 {
		return fmt.Errorf("QuantityPerOrder must be > 0")
	}

	// MaxPositionQty must be > 0
	if req.MaxPositionQty <= 0 {
		return fmt.Errorf("MaxPositionQty must be > 0")
	}

	return nil
}
