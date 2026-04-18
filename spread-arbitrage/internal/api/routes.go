package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"spread-arbitrage/internal/engine"
	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
	"spread-arbitrage/internal/wsmanager"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Handler struct {
	engine  *engine.Engine
	taskMgr *engine.TaskManager
	clients map[string]exchange.Client
	hub     *Hub
	wsMgr   *wsmanager.Manager
}

func NewHandler(eng *engine.Engine, tm *engine.TaskManager, clients map[string]exchange.Client, hub *Hub, wsMgr *wsmanager.Manager) *Handler {
	return &Handler{engine: eng, taskMgr: tm, clients: clients, hub: hub, wsMgr: wsMgr}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	api.GET("/tasks", h.ListTasks)
	api.POST("/tasks", h.CreateTask)
	api.PUT("/tasks/:id", h.UpdateTask)
	api.DELETE("/tasks/:id", h.DeleteTask)
	api.POST("/tasks/:id/start", h.StartTask)
	api.POST("/tasks/:id/stop", h.StopTask)
	api.POST("/tasks/:id/open", h.ManualOpen)
	api.POST("/tasks/:id/close", h.ManualClose)
	api.GET("/tasks/:id/positions", h.GetPositions)
	api.GET("/tasks/:id/trades", h.GetTrades)

	// Market data subscription (independent of tasks)
	api.POST("/subscribe", h.Subscribe)
	api.POST("/unsubscribe", h.Unsubscribe)

	// Test trading connectivity
	api.POST("/test-trade/:exchange", h.TestTrade)

	// Query positions/trades/funding by exchange+symbol directly (no task required)
	api.GET("/positions/all/:symbol", h.GetAllPositions)
	api.GET("/positions/:exchange/:symbol", h.GetPositionsByExchange)
	api.GET("/orders/:exchange/:symbol", h.GetOrdersByExchange)
	api.GET("/funding/:exchange/:symbol", h.GetFundingRate)

	r.GET("/ws", h.HandleWebSocket)
}

// ListTasks returns all tasks with current position quantities
func (h *Handler) ListTasks(c *gin.Context) {
	tasks := h.taskMgr.List()

	// Enhance each task with current position quantity
	type TaskWithPosition struct {
		*model.Task
		CurrentPosition float64 `json:"current_position"`
	}

	result := make([]TaskWithPosition, len(tasks))
	for i, task := range tasks {
		result[i] = TaskWithPosition{
			Task:            task,
			CurrentPosition: h.engine.GetPositionQty(task.ID),
		}
	}

	c.JSON(http.StatusOK, result)
}

// CreateTask creates a new task
func (h *Handler) CreateTask(c *gin.Context) {
	var req model.TaskCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task, err := h.taskMgr.Create(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, task)
}

// UpdateTask updates an existing task
func (h *Handler) UpdateTask(c *gin.Context) {
	id := c.Param("id")

	var req model.TaskCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task, err := h.taskMgr.Update(id, req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, task)
}

// DeleteTask deletes a task
func (h *Handler) DeleteTask(c *gin.Context) {
	id := c.Param("id")

	if err := h.taskMgr.Delete(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "task deleted"})
}

// StartTask starts a task
func (h *Handler) StartTask(c *gin.Context) {
	id := c.Param("id")
	ctx := context.Background()

	if err := h.engine.StartTask(ctx, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "task started"})
}

// StopTask stops a task
func (h *Handler) StopTask(c *gin.Context) {
	id := c.Param("id")

	if err := h.engine.StopTask(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "task stopped"})
}

// ManualOpen executes a manual open trade
func (h *Handler) ManualOpen(c *gin.Context) {
	id := c.Param("id")
	ctx := context.Background()

	if err := h.engine.ManualOpen(ctx, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "manual open executed"})
}

// ManualClose executes a manual close trade
func (h *Handler) ManualClose(c *gin.Context) {
	id := c.Param("id")
	ctx := context.Background()

	if err := h.engine.ManualClose(ctx, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "manual close executed"})
}

// GetPositions returns positions from both exchanges for a task
func (h *Handler) GetPositions(c *gin.Context) {
	id := c.Param("id")
	ctx := context.Background()

	task, err := h.taskMgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Get positions from both exchanges
	clientA, okA := h.clients[task.ExchangeA]
	clientB, okB := h.clients[task.ExchangeB]

	if !okA || !okB {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "exchange client not found"})
		return
	}

	posA, errA := clientA.GetPosition(ctx, task.Symbol)
	posB, errB := clientB.GetPosition(ctx, task.Symbol)

	positions := []interface{}{}

	if errA != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errA.Error()})
		return
	}
	if errB != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errB.Error()})
		return
	}

	positions = append(positions, posA, posB)
	c.JSON(http.StatusOK, positions)
}

// GetTrades returns orders from both exchanges for a task, sorted by timestamp desc
func (h *Handler) GetTrades(c *gin.Context) {
	id := c.Param("id")
	ctx := context.Background()

	task, err := h.taskMgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	clientA, okA := h.clients[task.ExchangeA]
	clientB, okB := h.clients[task.ExchangeB]
	if !okA || !okB {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "exchange client not found"})
		return
	}

	ordersA, errA := clientA.GetOrders(ctx, task.Symbol)
	ordersB, errB := clientB.GetOrders(ctx, task.Symbol)
	if errA != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errA.Error()})
		return
	}
	if errB != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errB.Error()})
		return
	}

	allOrders := append(ordersA, ordersB...)
	sort.Slice(allOrders, func(i, j int) bool {
		return allOrders[i].Timestamp.After(allOrders[j].Timestamp)
	})

	c.JSON(http.StatusOK, allOrders)
}

// Subscribe subscribes to market data for an exchange+symbol pair
func (h *Handler) Subscribe(c *gin.Context) {
	var req struct {
		Exchange string `json:"exchange" binding:"required"`
		Symbol   string `json:"symbol" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, ok := h.clients[req.Exchange]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown exchange: " + req.Exchange})
		return
	}
	ctx := context.Background()
	if err := h.wsMgr.Subscribe(ctx, req.Exchange, req.Symbol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Also subscribe to depth
	if err := h.wsMgr.SubscribeDepth(ctx, req.Exchange, req.Symbol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "subscribed", "exchange": req.Exchange, "symbol": req.Symbol})
}

// Unsubscribe unsubscribes from market data
func (h *Handler) Unsubscribe(c *gin.Context) {
	var req struct {
		Exchange string `json:"exchange" binding:"required"`
		Symbol   string `json:"symbol" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.wsMgr.Unsubscribe(req.Exchange, req.Symbol)
	h.wsMgr.UnsubscribeDepth(req.Exchange, req.Symbol)
	c.JSON(http.StatusOK, gin.H{"message": "unsubscribed"})
}

// GetAllPositions returns positions from all exchanges for a symbol in one call
func (h *Handler) GetAllPositions(c *gin.Context) {
	symbol := c.Param("symbol")
	ctx := context.Background()

	var mu sync.Mutex
	var positions []*model.Position
	var wg sync.WaitGroup

	for name, client := range h.clients {
		wg.Add(1)
		go func(exName string, cl exchange.Client) {
			defer wg.Done()
			pos, err := cl.GetPosition(ctx, symbol)
			if err != nil {
				pos = &model.Position{Exchange: exName, Symbol: symbol}
			}
			mu.Lock()
			positions = append(positions, pos)
			mu.Unlock()
		}(name, client)
	}
	wg.Wait()

	c.JSON(http.StatusOK, positions)
}

// GetPositionsByExchange returns position for a specific exchange+symbol
func (h *Handler) GetPositionsByExchange(c *gin.Context) {
	exchangeName := c.Param("exchange")
	symbol := c.Param("symbol")
	ctx := context.Background()

	client, ok := h.clients[exchangeName]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown exchange: " + exchangeName})
		return
	}

	pos, err := client.GetPosition(ctx, symbol)
	if err != nil {
		// Return empty position instead of 500 (API key may not be configured)
		c.JSON(http.StatusOK, &model.Position{
			Exchange: exchangeName,
			Symbol:   symbol,
		})
		return
	}

	c.JSON(http.StatusOK, pos)
}

// GetOrdersByExchange returns orders (委托记录) for a specific exchange+symbol
func (h *Handler) GetOrdersByExchange(c *gin.Context) {
	exchangeName := c.Param("exchange")
	symbol := c.Param("symbol")
	ctx := context.Background()

	client, ok := h.clients[exchangeName]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown exchange: " + exchangeName})
		return
	}

	orders, err := client.GetOrders(ctx, symbol)
	if err != nil {
		c.JSON(http.StatusOK, []model.Order{})
		return
	}

	c.JSON(http.StatusOK, orders)
}

// TestTrade performs a small buy+sell to verify exchange connectivity and trading permissions.
// Uses DOGEUSDT with minimum quantity (~5 USDT).
func (h *Handler) TestTrade(c *gin.Context) {
	exchangeName := c.Param("exchange")
	ctx := context.Background()

	client, ok := h.clients[exchangeName]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown exchange: " + exchangeName})
		return
	}

	// Test parameters: DOGEUSDT, small quantity
	// Binance/Aster: min notional 5 USDT, DOGE ~$0.1, so 55 DOGE ≈ $5.5
	// OKX: DOGE-USDT-SWAP ctVal=1000, minSz=0.01 → 10 DOGE ≈ $1
	symbol := "DOGEUSDT"
	var qty float64
	if exchangeName == "okx" {
		qty = 10
	} else {
		qty = 55
	}

	type testResult struct {
		Exchange      string      `json:"exchange"`
		Success       bool        `json:"success"`
		Symbol        string      `json:"symbol"`
		Quantity      float64     `json:"quantity"`
		BuyOrder      interface{} `json:"buy_order,omitempty"`
		SellOrder     interface{} `json:"sell_order,omitempty"`
		Error         string      `json:"error,omitempty"`
		Steps         []string    `json:"steps"`
		PositionCheck interface{} `json:"position_check,omitempty"`
	}

	result := testResult{
		Exchange: exchangeName,
		Symbol:   symbol,
		Quantity: qty,
		Steps:    []string{},
	}

	// Step 0: Test read-only access first (GetPosition)
	result.Steps = append(result.Steps, "Testing API key with read-only request (GetPosition)...")
	pos, posErr := client.GetPosition(ctx, symbol)
	if posErr != nil {
		result.Steps = append(result.Steps, "READ-ONLY TEST FAILED: "+posErr.Error())
		result.Steps = append(result.Steps, "Possible causes: invalid API key, IP whitelist restriction, or missing Futures permission")
		result.Error = "API key validation failed: " + posErr.Error()
		c.JSON(http.StatusOK, result)
		return
	}
	result.PositionCheck = pos
	result.Steps = append(result.Steps, fmt.Sprintf("READ-ONLY OK: position side=%s size=%.4f", pos.Side, pos.Size))

	var err error

	// Step 1: Buy (prefer WS, fallback REST)
	result.Steps = append(result.Steps, "Placing BUY order...")
	var buyOrder *model.Order
	if wsPlacer, ok := client.(exchange.WSOrderPlacer); ok {
		result.Steps = append(result.Steps, "Using WebSocket order placement")
		buyOrder, err = wsPlacer.PlaceMarketOrderWS(ctx, symbol, "BUY", qty, "sa-test-buy")
		if err != nil {
			result.Steps = append(result.Steps, "WS BUY failed, falling back to REST: "+err.Error())
			buyOrder, err = client.PlaceMarketOrder(ctx, symbol, "BUY", qty, "sa-test-buy")
		}
	} else {
		buyOrder, err = client.PlaceMarketOrder(ctx, symbol, "BUY", qty, "sa-test-buy")
	}
	if err != nil {
		result.Error = "BUY failed: " + err.Error()
		result.Steps = append(result.Steps, "BUY FAILED: "+err.Error())
		c.JSON(http.StatusOK, result)
		return
	}
	result.BuyOrder = buyOrder
	result.Steps = append(result.Steps, "BUY OK: orderId="+buyOrder.OrderID)

	// Step 2: Sell (prefer WS, fallback REST)
	result.Steps = append(result.Steps, "Placing SELL order to close...")
	var sellOrder *model.Order
	if wsPlacer, ok := client.(exchange.WSOrderPlacer); ok {
		sellOrder, err = wsPlacer.PlaceMarketOrderWS(ctx, symbol, "SELL", qty, "sa-test-sell")
		if err != nil {
			result.Steps = append(result.Steps, "WS SELL failed, falling back to REST: "+err.Error())
			sellOrder, err = client.PlaceMarketOrder(ctx, symbol, "SELL", qty, "sa-test-sell")
		}
	} else {
		sellOrder, err = client.PlaceMarketOrder(ctx, symbol, "SELL", qty, "sa-test-sell")
	}
	if err != nil {
		result.Error = "SELL failed (position may remain open!): " + err.Error()
		result.Steps = append(result.Steps, "SELL FAILED: "+err.Error())
		c.JSON(http.StatusOK, result)
		return
	}
	result.SellOrder = sellOrder
	result.Steps = append(result.Steps, "SELL OK: orderId="+sellOrder.OrderID)

	result.Success = true
	result.Steps = append(result.Steps, "Test complete: account can trade on "+exchangeName)
	c.JSON(http.StatusOK, result)
}

// GetFundingRate returns funding rate for a specific exchange+symbol
func (h *Handler) GetFundingRate(c *gin.Context) {
	exchangeName := c.Param("exchange")
	symbol := c.Param("symbol")
	ctx := context.Background()

	client, ok := h.clients[exchangeName]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown exchange: " + exchangeName})
		return
	}

	fr, err := client.GetFundingRate(ctx, symbol)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"exchange": exchangeName,
			"symbol":   symbol,
			"rate":     0,
			"error":    err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, fr)
}

// HandleWebSocket upgrades connection and registers with hub
func (h *Handler) HandleWebSocket(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Register connection with hub (returns a write-safe wrapper)
	wc := h.hub.Register(conn)

	// Read loop to detect client disconnect
	go func() {
		defer h.hub.Unregister(wc)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}
