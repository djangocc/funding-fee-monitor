package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"spread-arbitrage/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// SubscribeUserData implements User Data Stream for Binance Futures
// 1. POST to get listenKey (apiKey in header, no signature)
// 2. Connect WS: wss://fstream.binance.com/ws/{listenKey}
// 3. Read loop: parse ORDER_TRADE_UPDATE and ACCOUNT_UPDATE events
// 4. Keepalive loop: PUT every 30 minutes
// 5. Auto-reconnect on disconnect (3s delay, max 10 retries)
func (c *BinanceClient) SubscribeUserData(ctx context.Context, callbacks UserDataCallbacks) error {
	if c.apiKey == "" {
		return fmt.Errorf("API key not configured")
	}

	// Determine listenKey endpoint based on baseURL
	listenKeyEndpoint := "/fapi/v1/listenKey"
	if strings.Contains(c.baseURL, "papi") {
		listenKeyEndpoint = "/papi/v1/listenKey"
	}

	// Get listenKey
	listenKey, err := c.getListenKey(listenKeyEndpoint)
	if err != nil {
		return fmt.Errorf("get listen key: %w", err)
	}

	log.Printf("[%s] USER DATA obtained listenKey=%s", c.name, listenKey)

	// Start keepalive goroutine
	go c.keepAliveListenKey(ctx, listenKeyEndpoint, listenKey)

	// Start WebSocket read loop with auto-reconnect
	go c.handleUserDataWS(ctx, listenKey, callbacks)

	return nil
}

// getListenKey requests a new listenKey from Binance
func (c *BinanceClient) getListenKey(endpoint string) (string, error) {
	reqURL := c.baseURL + endpoint

	req, err := http.NewRequest("POST", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	return result.ListenKey, nil
}

// keepAliveListenKey sends PUT requests every 30 minutes to keep the listenKey alive
func (c *BinanceClient) keepAliveListenKey(ctx context.Context, endpoint, listenKey string) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] USER DATA keepalive stopped", c.name)
			return
		case <-ticker.C:
			reqURL := c.baseURL + endpoint + "?listenKey=" + listenKey
			req, err := http.NewRequest("PUT", reqURL, nil)
			if err != nil {
				log.Printf("[%s] USER DATA keepalive create request error: %v", c.name, err)
				continue
			}

			req.Header.Set("X-MBX-APIKEY", c.apiKey)

			resp, err := c.httpClient.Do(req)
			if err != nil {
				log.Printf("[%s] USER DATA keepalive request error: %v", c.name, err)
				continue
			}

			if resp.StatusCode == http.StatusOK {
				log.Printf("[%s] USER DATA keepalive success", c.name)
			} else {
				body, _ := io.ReadAll(resp.Body)
				log.Printf("[%s] USER DATA keepalive failed (%d): %s", c.name, resp.StatusCode, string(body))
			}
			resp.Body.Close()
		}
	}
}

// handleUserDataWS manages WebSocket connection with auto-reconnect
func (c *BinanceClient) handleUserDataWS(ctx context.Context, listenKey string, callbacks UserDataCallbacks) {
	maxRetries := 10
	retryDelay := 3 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		select {
		case <-ctx.Done():
			log.Printf("[%s] USER DATA context cancelled", c.name)
			return
		default:
		}

		var wsURL string
		if strings.Contains(c.baseURL, "papi") {
			wsURL = fmt.Sprintf("wss://fstream.binance.com/pm/ws/%s", listenKey)
		} else {
			wsURL = fmt.Sprintf("wss://fstream.binance.com/ws/%s", listenKey)
		}
		log.Printf("[%s] USER DATA connecting to %s (attempt %d/%d)", c.name, wsURL, retry+1, maxRetries)

		conn, _, err := c.wsDialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("[%s] USER DATA dial error: %v", c.name, err)
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		log.Printf("[%s] USER DATA connected", c.name)

		// Read messages until error or context cancellation
		disconnected := c.readUserDataMessages(ctx, conn, callbacks)
		conn.Close()

		if !disconnected {
			// Context cancelled, exit gracefully
			log.Printf("[%s] USER DATA disconnected gracefully", c.name)
			return
		}

		// Connection lost, retry
		log.Printf("[%s] USER DATA disconnected, retrying in %v", c.name, retryDelay)
		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	log.Printf("[%s] USER DATA max retries reached, giving up", c.name)
}

// readUserDataMessages reads and parses User Data Stream messages
func (c *BinanceClient) readUserDataMessages(ctx context.Context, conn *websocket.Conn, callbacks UserDataCallbacks) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[%s] USER DATA read error: %v", c.name, err)
			return true // disconnected
		}

		// Parse as raw map for case-sensitive field extraction
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			log.Printf("[%s] USER DATA parse error: %v", c.name, err)
			continue
		}

		// Extract event type "e"
		var eventType string
		if e, ok := raw["e"]; ok {
			json.Unmarshal(e, &eventType)
		}

		switch eventType {
		case "ORDER_TRADE_UPDATE":
			c.handleOrderUpdate(raw, callbacks)
		case "ACCOUNT_UPDATE":
			c.handleAccountUpdate(raw, callbacks)
		default:
			// Ignore other events (listenKeyExpired, etc.)
		}
	}
}

// handleOrderUpdate parses ORDER_TRADE_UPDATE event
// Event format: {"e":"ORDER_TRADE_UPDATE","T":1234567890123,"o":{"s":"BTCUSDT","c":"sa-xxxx-A","S":"BUY","X":"FILLED","x":"TRADE","q":"1.0","z":"1.0","ap":"50000.0","i":12345,...}}
func (c *BinanceClient) handleOrderUpdate(raw map[string]json.RawMessage, callbacks UserDataCallbacks) {
	if callbacks.OnOrderUpdate == nil {
		return
	}

	// Extract timestamp "T" (transaction time)
	var timestamp int64
	if T, ok := raw["T"]; ok {
		json.Unmarshal(T, &timestamp)
	}

	// Extract order object "o"
	var orderRaw map[string]json.RawMessage
	if o, ok := raw["o"]; ok {
		json.Unmarshal(o, &orderRaw)
	} else {
		return
	}

	// Extract order fields
	var symbol, clientOrderID, side, status, execType string
	var orderID int64
	var filledQty, avgPrice float64

	if s, ok := orderRaw["s"]; ok {
		json.Unmarshal(s, &symbol)
	}
	if c, ok := orderRaw["c"]; ok {
		json.Unmarshal(c, &clientOrderID)
	}
	if S, ok := orderRaw["S"]; ok {
		json.Unmarshal(S, &side)
	}
	if X, ok := orderRaw["X"]; ok {
		json.Unmarshal(X, &status)
	}
	if x, ok := orderRaw["x"]; ok {
		json.Unmarshal(x, &execType)
	}
	if z, ok := orderRaw["z"]; ok {
		var zStr string
		json.Unmarshal(z, &zStr)
		filledQty, _ = strconv.ParseFloat(zStr, 64)
	}
	if ap, ok := orderRaw["ap"]; ok {
		var apStr string
		json.Unmarshal(ap, &apStr)
		avgPrice, _ = strconv.ParseFloat(apStr, 64)
	}
	if i, ok := orderRaw["i"]; ok {
		json.Unmarshal(i, &orderID)
	}

	update := model.OrderUpdate{
		Exchange:      c.name,
		Symbol:        symbol,
		OrderID:       fmt.Sprintf("%d", orderID),
		ClientOrderID: clientOrderID,
		Side:          side,
		Status:        status,
		ExecType:      execType,
		FilledQty:     filledQty,
		AvgPrice:      avgPrice,
		Timestamp:     time.UnixMilli(timestamp),
	}

	log.Printf("[%s] USER DATA ORDER UPDATE: %s %s %s qty=%.8f price=%.8f status=%s exec=%s clientId=%s",
		c.name, symbol, side, update.OrderID, filledQty, avgPrice, status, execType, clientOrderID)

	callbacks.OnOrderUpdate(update)
}

// handleAccountUpdate parses ACCOUNT_UPDATE event
// Event format: {"e":"ACCOUNT_UPDATE","T":1234567890123,"a":{"m":"ORDER","P":[{"s":"BTCUSDT","pa":"4.0","ps":"LONG",...}]}}
func (c *BinanceClient) handleAccountUpdate(raw map[string]json.RawMessage, callbacks UserDataCallbacks) {
	if callbacks.OnAccountUpdate == nil {
		return
	}

	// Extract timestamp "T"
	var timestamp int64
	if T, ok := raw["T"]; ok {
		json.Unmarshal(T, &timestamp)
	}

	// Extract account object "a"
	var accountRaw map[string]json.RawMessage
	if a, ok := raw["a"]; ok {
		json.Unmarshal(a, &accountRaw)
	} else {
		return
	}

	// Extract reason "m"
	var reason string
	if m, ok := accountRaw["m"]; ok {
		json.Unmarshal(m, &reason)
	}

	// Extract positions array "P"
	var positions []map[string]json.RawMessage
	if P, ok := accountRaw["P"]; ok {
		json.Unmarshal(P, &positions)
	}

	// Process each position
	for _, posRaw := range positions {
		var symbol, positionSide string
		var positionAmt float64

		if s, ok := posRaw["s"]; ok {
			json.Unmarshal(s, &symbol)
		}
		if ps, ok := posRaw["ps"]; ok {
			json.Unmarshal(ps, &positionSide)
		}
		if pa, ok := posRaw["pa"]; ok {
			var paStr string
			json.Unmarshal(pa, &paStr)
			positionAmt, _ = strconv.ParseFloat(paStr, 64)
		}

		update := model.AccountUpdate{
			Exchange:  c.name,
			Symbol:    symbol,
			Side:      positionSide,
			Size:      positionAmt,
			Reason:    reason,
			Timestamp: time.UnixMilli(timestamp),
		}

		log.Printf("[%s] USER DATA ACCOUNT UPDATE: %s side=%s size=%.8f reason=%s",
			c.name, symbol, positionSide, positionAmt, reason)

		callbacks.OnAccountUpdate(update)
	}
}
