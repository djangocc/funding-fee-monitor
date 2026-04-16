package api

import (
	"context"
	"net/http"
	"sort"

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

	// Query positions/trades by exchange+symbol directly (no task required)
	api.GET("/positions/:exchange/:symbol", h.GetPositionsByExchange)
	api.GET("/trades/:exchange/:symbol", h.GetTradesByExchange)

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

// GetTrades returns trades from both exchanges for a task, sorted by timestamp desc
func (h *Handler) GetTrades(c *gin.Context) {
	id := c.Param("id")
	ctx := context.Background()

	task, err := h.taskMgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Get trades from both exchanges
	clientA, okA := h.clients[task.ExchangeA]
	clientB, okB := h.clients[task.ExchangeB]

	if !okA || !okB {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "exchange client not found"})
		return
	}

	tradesA, errA := clientA.GetTrades(ctx, task.Symbol)
	tradesB, errB := clientB.GetTrades(ctx, task.Symbol)

	if errA != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errA.Error()})
		return
	}
	if errB != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errB.Error()})
		return
	}

	// Merge and sort by timestamp descending
	allTrades := append(tradesA, tradesB...)
	sort.Slice(allTrades, func(i, j int) bool {
		return allTrades[i].Timestamp.After(allTrades[j].Timestamp)
	})

	c.JSON(http.StatusOK, allTrades)
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

// GetTradesByExchange returns trades for a specific exchange+symbol
func (h *Handler) GetTradesByExchange(c *gin.Context) {
	exchangeName := c.Param("exchange")
	symbol := c.Param("symbol")
	ctx := context.Background()

	client, ok := h.clients[exchangeName]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown exchange: " + exchangeName})
		return
	}

	trades, err := client.GetTrades(ctx, symbol)
	if err != nil {
		// Return empty array instead of 500 (API key may not be configured)
		c.JSON(http.StatusOK, []model.Trade{})
		return
	}

	c.JSON(http.StatusOK, trades)
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
