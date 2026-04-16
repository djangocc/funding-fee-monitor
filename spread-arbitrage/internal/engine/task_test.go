package engine

import (
	"spread-arbitrage/internal/model"
	"testing"
)

func validShortSpreadReq() model.TaskCreateRequest {
	return model.TaskCreateRequest{
		Symbol:           "RAVEUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "aster",
		Direction:        model.ShortSpread,
		OpenThreshold:    0.5,
		CloseThreshold:   0.1,
		ConfirmCount:     3,
		QuantityPerOrder: 100,
		MaxPositionQty:   500,
		DataMaxLatencyMs: 1000,
	}
}

func validLongSpreadReq() model.TaskCreateRequest {
	return model.TaskCreateRequest{
		Symbol:           "RAVEUSDT",
		ExchangeA:        "binance",
		ExchangeB:        "aster",
		Direction:        model.LongSpread,
		OpenThreshold:    -0.5,
		CloseThreshold:   -0.1,
		ConfirmCount:     3,
		QuantityPerOrder: 100,
		MaxPositionQty:   500,
		DataMaxLatencyMs: 1000,
	}
}

func TestTaskManager_CreateAndGet(t *testing.T) {
	tm := NewTaskManager()
	req := validShortSpreadReq()

	// Create task
	task, err := tm.Create(req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify task has an ID
	if task.ID == "" {
		t.Error("Task ID should not be empty")
	}

	// Verify initial status is stopped
	if task.Status != model.StatusStopped {
		t.Errorf("Expected status %s, got %s", model.StatusStopped, task.Status)
	}

	// Get task by ID
	retrieved, err := tm.Get(task.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Verify retrieved task matches created task
	if retrieved.ID != task.ID {
		t.Errorf("Expected ID %s, got %s", task.ID, retrieved.ID)
	}
	if retrieved.Symbol != req.Symbol {
		t.Errorf("Expected Symbol %s, got %s", req.Symbol, retrieved.Symbol)
	}
	if retrieved.ExchangeA != req.ExchangeA {
		t.Errorf("Expected ExchangeA %s, got %s", req.ExchangeA, retrieved.ExchangeA)
	}
	if retrieved.ExchangeB != req.ExchangeB {
		t.Errorf("Expected ExchangeB %s, got %s", req.ExchangeB, retrieved.ExchangeB)
	}
	if retrieved.Direction != req.Direction {
		t.Errorf("Expected Direction %s, got %s", req.Direction, retrieved.Direction)
	}
}

func TestTaskManager_List(t *testing.T) {
	tm := NewTaskManager()

	// Create 3 tasks
	req1 := validShortSpreadReq()
	req2 := validLongSpreadReq()
	req3 := validShortSpreadReq()
	req3.Symbol = "BTCUSDT"

	task1, err := tm.Create(req1)
	if err != nil {
		t.Fatalf("Create task1 failed: %v", err)
	}
	task2, err := tm.Create(req2)
	if err != nil {
		t.Fatalf("Create task2 failed: %v", err)
	}
	task3, err := tm.Create(req3)
	if err != nil {
		t.Fatalf("Create task3 failed: %v", err)
	}

	// List all tasks
	tasks := tm.List()

	// Verify count
	if len(tasks) != 3 {
		t.Fatalf("Expected 3 tasks, got %d", len(tasks))
	}

	// Verify all task IDs are present
	ids := make(map[string]bool)
	for _, task := range tasks {
		ids[task.ID] = true
	}

	if !ids[task1.ID] {
		t.Errorf("Task1 ID %s not found in list", task1.ID)
	}
	if !ids[task2.ID] {
		t.Errorf("Task2 ID %s not found in list", task2.ID)
	}
	if !ids[task3.ID] {
		t.Errorf("Task3 ID %s not found in list", task3.ID)
	}
}

func TestTaskManager_Update(t *testing.T) {
	tm := NewTaskManager()
	req := validShortSpreadReq()

	// Create task
	task, err := tm.Create(req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update with different threshold
	updateReq := req
	updateReq.OpenThreshold = 0.8
	updateReq.CloseThreshold = 0.2

	updated, err := tm.Update(task.ID, updateReq)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify changes
	if updated.OpenThreshold != 0.8 {
		t.Errorf("Expected OpenThreshold 0.8, got %f", updated.OpenThreshold)
	}
	if updated.CloseThreshold != 0.2 {
		t.Errorf("Expected CloseThreshold 0.2, got %f", updated.CloseThreshold)
	}

	// Verify changes persisted by getting task again
	retrieved, err := tm.Get(task.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.OpenThreshold != 0.8 {
		t.Errorf("OpenThreshold not persisted, got %f", retrieved.OpenThreshold)
	}
	if retrieved.CloseThreshold != 0.2 {
		t.Errorf("CloseThreshold not persisted, got %f", retrieved.CloseThreshold)
	}
}

func TestTaskManager_Delete(t *testing.T) {
	tm := NewTaskManager()

	// Test deleting stopped task (should succeed)
	req := validShortSpreadReq()
	task1, err := tm.Create(req)
	if err != nil {
		t.Fatalf("Create task1 failed: %v", err)
	}

	err = tm.Delete(task1.ID)
	if err != nil {
		t.Errorf("Delete stopped task should succeed, got error: %v", err)
	}

	// Verify task is deleted
	_, err = tm.Get(task1.ID)
	if err == nil {
		t.Error("Get should fail for deleted task")
	}

	// Test deleting running task (should fail)
	task2, err := tm.Create(req)
	if err != nil {
		t.Fatalf("Create task2 failed: %v", err)
	}

	// Set task to running
	err = tm.SetStatus(task2.ID, model.StatusRunning)
	if err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	// Attempt to delete running task
	err = tm.Delete(task2.ID)
	if err == nil {
		t.Error("Delete running task should return error")
	}

	// Verify task still exists
	retrieved, err := tm.Get(task2.ID)
	if err != nil {
		t.Error("Running task should not be deleted")
	}
	if retrieved.Status != model.StatusRunning {
		t.Errorf("Expected status %s, got %s", model.StatusRunning, retrieved.Status)
	}
}

func TestTaskManager_Validation_ShortSpread(t *testing.T) {
	tm := NewTaskManager()

	// Valid: OpenThreshold > CloseThreshold
	req := validShortSpreadReq()
	req.OpenThreshold = 0.5
	req.CloseThreshold = 0.1
	_, err := tm.Create(req)
	if err != nil {
		t.Errorf("Valid short_spread should succeed: %v", err)
	}

	// Invalid: OpenThreshold < CloseThreshold
	req.OpenThreshold = 0.1
	req.CloseThreshold = 0.5
	_, err = tm.Create(req)
	if err == nil {
		t.Error("short_spread with OpenThreshold < CloseThreshold should fail")
	}

	// Invalid: OpenThreshold == CloseThreshold
	req.OpenThreshold = 0.3
	req.CloseThreshold = 0.3
	_, err = tm.Create(req)
	if err == nil {
		t.Error("short_spread with OpenThreshold == CloseThreshold should fail")
	}
}

func TestTaskManager_Validation_LongSpread(t *testing.T) {
	tm := NewTaskManager()

	// Valid: OpenThreshold < CloseThreshold
	req := validLongSpreadReq()
	req.OpenThreshold = -0.5
	req.CloseThreshold = -0.1
	_, err := tm.Create(req)
	if err != nil {
		t.Errorf("Valid long_spread should succeed: %v", err)
	}

	// Invalid: OpenThreshold > CloseThreshold
	req.OpenThreshold = -0.1
	req.CloseThreshold = -0.5
	_, err = tm.Create(req)
	if err == nil {
		t.Error("long_spread with OpenThreshold > CloseThreshold should fail")
	}

	// Invalid: OpenThreshold == CloseThreshold
	req.OpenThreshold = -0.3
	req.CloseThreshold = -0.3
	_, err = tm.Create(req)
	if err == nil {
		t.Error("long_spread with OpenThreshold == CloseThreshold should fail")
	}
}

func TestTaskManager_Validation_SameExchange(t *testing.T) {
	tm := NewTaskManager()
	req := validShortSpreadReq()

	// Same exchange should fail
	req.ExchangeA = "binance"
	req.ExchangeB = "binance"
	_, err := tm.Create(req)
	if err == nil {
		t.Error("Same ExchangeA and ExchangeB should fail")
	}
}

func TestTaskManager_Validation_ConfirmCount(t *testing.T) {
	tm := NewTaskManager()
	req := validShortSpreadReq()

	// ConfirmCount < 1 should fail
	req.ConfirmCount = 0
	_, err := tm.Create(req)
	if err == nil {
		t.Error("ConfirmCount < 1 should fail")
	}

	req.ConfirmCount = -1
	_, err = tm.Create(req)
	if err == nil {
		t.Error("ConfirmCount < 1 should fail")
	}

	// ConfirmCount >= 1 should succeed
	req.ConfirmCount = 1
	_, err = tm.Create(req)
	if err != nil {
		t.Errorf("ConfirmCount >= 1 should succeed: %v", err)
	}
}

func TestTaskManager_Validation_Quantity(t *testing.T) {
	tm := NewTaskManager()
	req := validShortSpreadReq()

	// QuantityPerOrder <= 0 should fail
	req.QuantityPerOrder = 0
	_, err := tm.Create(req)
	if err == nil {
		t.Error("QuantityPerOrder <= 0 should fail")
	}

	req.QuantityPerOrder = -10
	_, err = tm.Create(req)
	if err == nil {
		t.Error("QuantityPerOrder <= 0 should fail")
	}

	// MaxPositionQty <= 0 should fail
	req.QuantityPerOrder = 100
	req.MaxPositionQty = 0
	_, err = tm.Create(req)
	if err == nil {
		t.Error("MaxPositionQty <= 0 should fail")
	}

	req.MaxPositionQty = -100
	_, err = tm.Create(req)
	if err == nil {
		t.Error("MaxPositionQty <= 0 should fail")
	}

	// Both > 0 should succeed
	req.MaxPositionQty = 500
	_, err = tm.Create(req)
	if err != nil {
		t.Errorf("Valid quantities should succeed: %v", err)
	}
}
