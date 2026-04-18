package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"spread-arbitrage/internal/model"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Package-level storage for per-client WebSocket connections
var wsOrderConns sync.Map // key: client name (string) → value: *wsOrderConn

// wsOrderConn manages a single WS connection for order placement
type wsOrderConn struct {
	conn        *websocket.Conn
	mu          sync.Mutex // protects writes to conn
	responses   map[string]chan json.RawMessage
	responsesMu sync.Mutex
	closed      bool
	closedMu    sync.Mutex
}

// PlaceMarketOrderWS places a market order via WebSocket
func (c *BinanceClient) PlaceMarketOrderWS(ctx context.Context, symbol, side string, qty float64, clientOrderID string) (*model.Order, error) {
	if strings.Contains(c.baseURL, "papi") {
		return nil, fmt.Errorf("Portfolio Margin does not support WS order API")
	}

	log.Printf("[%s] WS ORDER REQUEST: %s %s qty=%.8f clientOrderId=%s", c.name, side, symbol, qty, clientOrderID)

	// Get or create connection
	wc, err := c.getOrCreateWSOrderConn(ctx)
	if err != nil {
		log.Printf("[%s] WS ORDER: failed to get connection, will fall back to REST: %v", c.name, err)
		return nil, fmt.Errorf("ws connection failed: %w", err)
	}

	// Build order request (each request carries its own apiKey + signature, no session.logon needed)
	requestID := uuid.New().String()
	params := c.buildOrderParams(symbol, side, qty, clientOrderID)

	// Sign the params
	signature := c.signOrderParams(params)
	params["signature"] = signature

	request := map[string]interface{}{
		"id":     requestID,
		"method": "order.place",
		"params": params,
	}

	// Create response channel
	respCh := make(chan json.RawMessage, 1)
	wc.responsesMu.Lock()
	wc.responses[requestID] = respCh
	wc.responsesMu.Unlock()

	defer func() {
		wc.responsesMu.Lock()
		delete(wc.responses, requestID)
		wc.responsesMu.Unlock()
	}()

	// Send request
	wc.mu.Lock()
	err = wc.conn.WriteJSON(request)
	wc.mu.Unlock()
	if err != nil {
		log.Printf("[%s] WS ORDER: failed to send request: %v", c.name, err)
		c.closeWSOrderConn()
		return nil, fmt.Errorf("ws send failed: %w", err)
	}

	// Wait for response with timeout
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		log.Printf("[%s] WS ORDER: timeout waiting for response", c.name)
		c.closeWSOrderConn()
		return nil, fmt.Errorf("ws order timeout")
	case rawResp := <-respCh:
		return c.parseWSOrderResponse(rawResp, symbol, side, qty, clientOrderID)
	}
}

// getOrCreateWSOrderConn returns the existing connection or creates a new one
func (c *BinanceClient) getOrCreateWSOrderConn(ctx context.Context) (*wsOrderConn, error) {
	// Try to load existing connection
	if v, ok := wsOrderConns.Load(c.name); ok {
		wc := v.(*wsOrderConn)
		wc.closedMu.Lock()
		closed := wc.closed
		wc.closedMu.Unlock()
		if !closed {
			return wc, nil
		}
		// Connection was closed, remove it
		wsOrderConns.Delete(c.name)
	}

	// Create new connection
	wsURL := c.getWSOrderURL()
	log.Printf("[%s] WS ORDER: connecting to %s", c.name, wsURL)

	conn, _, err := c.wsDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	wc := &wsOrderConn{
		conn:      conn,
		responses: make(map[string]chan json.RawMessage),
		closed:    false,
	}

	// Start read loop
	go c.wsOrderReadLoop(wc)

	// Store in map
	wsOrderConns.Store(c.name, wc)

	log.Printf("[%s] WS ORDER: connected successfully", c.name)
	return wc, nil
}

// getWSOrderURL returns the appropriate WebSocket URL for order placement
func (c *BinanceClient) getWSOrderURL() string {
	return "wss://ws-fapi.binance.com/ws-fapi/v1"
}

// buildOrderParams builds the order parameters map
func (c *BinanceClient) buildOrderParams(symbol, side string, qty float64, clientOrderID string) map[string]interface{} {
	params := map[string]interface{}{
		"symbol":    symbol,
		"side":      side,
		"type":      "MARKET",
		"quantity":  fmt.Sprintf("%.8f", qty),
		"timestamp": time.Now().UnixMilli(),
		"apiKey":    c.apiKey,
	}
	if clientOrderID != "" {
		params["newClientOrderId"] = clientOrderID
	}
	return params
}

// signOrderParams signs the order parameters
func (c *BinanceClient) signOrderParams(params map[string]interface{}) string {
	// Sort keys alphabetically
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build query string
	var parts []string
	for _, k := range keys {
		v := params[k]
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	queryString := strings.Join(parts, "&")

	// Compute HMAC-SHA256
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(queryString))
	return hex.EncodeToString(h.Sum(nil))
}

// parseWSOrderResponse parses the WebSocket order response
func (c *BinanceClient) parseWSOrderResponse(rawResp json.RawMessage, symbol, side string, qty float64, clientOrderID string) (*model.Order, error) {
	var resp struct {
		Status int    `json:"status"`
		Result *struct {
			Symbol        string `json:"symbol"`
			OrderID       int64  `json:"orderId"`
			ClientOrderID string `json:"clientOrderId"`
			Side          string `json:"side"`
			Type          string `json:"type"`
			Status        string `json:"status"`
			OrigQty       string `json:"origQty"`
			ExecutedQty   string `json:"executedQty"`
			AvgPrice      string `json:"avgPrice"`
			UpdateTime    int64  `json:"updateTime"`
		} `json:"result"`
		Error *struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		} `json:"error"`
	}

	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Printf("[%s] WS ORDER PARSE ERROR: %v, raw=%s", c.name, err, string(rawResp))
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if resp.Error != nil {
		log.Printf("[%s] WS ORDER ERROR: code=%d msg=%s", c.name, resp.Error.Code, resp.Error.Msg)
		return nil, fmt.Errorf("order error %d: %s", resp.Error.Code, resp.Error.Msg)
	}

	if resp.Status != 200 || resp.Result == nil {
		log.Printf("[%s] WS ORDER: unexpected status=%d or nil result, raw=%s", c.name, resp.Status, string(rawResp))
		return nil, fmt.Errorf("unexpected response status %d", resp.Status)
	}

	result := resp.Result
	avgPrice, _ := strconv.ParseFloat(result.AvgPrice, 64)
	executedQty, _ := strconv.ParseFloat(result.ExecutedQty, 64)

	// If avgPrice/executedQty are 0, fall back to requested quantity
	if executedQty == 0 {
		executedQty = qty
	}

	order := &model.Order{
		Exchange:      c.name,
		Symbol:        result.Symbol,
		Side:          result.Side,
		Quantity:      executedQty,
		Price:         avgPrice,
		OrderID:       fmt.Sprintf("%d", result.OrderID),
		ClientOrderID: clientOrderID,
		Status:        result.Status,
		Timestamp:     time.UnixMilli(result.UpdateTime),
	}

	log.Printf("[%s] WS ORDER FILLED: %s %s qty=%.8f avgPrice=%.8f orderId=%s clientOrderId=%s status=%s", c.name, order.Side, order.Symbol, order.Quantity, order.Price, order.OrderID, order.ClientOrderID, order.Status)
	return order, nil
}

// wsOrderReadLoop reads messages from the WebSocket connection
func (c *BinanceClient) wsOrderReadLoop(wc *wsOrderConn) {
	defer func() {
		wc.closedMu.Lock()
		wc.closed = true
		wc.closedMu.Unlock()

		// Close all pending response channels
		wc.responsesMu.Lock()
		for _, ch := range wc.responses {
			close(ch)
		}
		wc.responses = make(map[string]chan json.RawMessage)
		wc.responsesMu.Unlock()

		wc.conn.Close()
		wsOrderConns.Delete(c.name)
		log.Printf("[%s] WS ORDER: read loop exited, connection closed", c.name)
	}()

	for {
		_, message, err := wc.conn.ReadMessage()
		if err != nil {
			log.Printf("[%s] WS ORDER: read error: %v", c.name, err)
			return
		}

		// Parse message to extract ID
		var msg struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[%s] WS ORDER: failed to parse message ID: %v", c.name, err)
			continue
		}

		if msg.ID == "" {
			// Maybe a server-initiated message, log and skip
			log.Printf("[%s] WS ORDER: received message without ID: %s", c.name, string(message))
			continue
		}

		// Find matching response channel
		wc.responsesMu.Lock()
		ch, ok := wc.responses[msg.ID]
		wc.responsesMu.Unlock()

		if ok {
			select {
			case ch <- json.RawMessage(message):
			default:
				// Channel full or closed, skip
			}
		} else {
			log.Printf("[%s] WS ORDER: received response for unknown request ID: %s", c.name, msg.ID)
		}
	}
}

// closeWSOrderConn closes the WebSocket order connection for this client
func (c *BinanceClient) closeWSOrderConn() {
	if v, ok := wsOrderConns.LoadAndDelete(c.name); ok {
		wc := v.(*wsOrderConn)
		wc.closedMu.Lock()
		if !wc.closed {
			wc.closed = true
			wc.conn.Close()
		}
		wc.closedMu.Unlock()
		log.Printf("[%s] WS ORDER: connection closed", c.name)
	}
}
