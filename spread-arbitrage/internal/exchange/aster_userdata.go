package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"spread-arbitrage/internal/model"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// SubscribeUserData subscribes to User Data Stream for real-time order/position updates
func (c *AsterClient) SubscribeUserData(ctx context.Context, callbacks UserDataCallbacks) error {
	// Get listenKey
	listenKey, err := c.getListenKey()
	if err != nil {
		return fmt.Errorf("get listen key: %w", err)
	}

	log.Printf("[%s] USER DATA: listenKey=%s", c.name, listenKey)

	// Start keepalive goroutine
	go c.keepaliveListenKey(ctx, listenKey)

	// Connect to user data stream
	go c.handleUserDataStream(ctx, listenKey, callbacks)

	return nil
}

// getListenKey retrieves a new listenKey for the user data stream
func (c *AsterClient) getListenKey() (string, error) {
	body, err := c.doSignedRequest("POST", "/fapi/v3/listenKey", url.Values{})
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}

	var resp struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if resp.ListenKey == "" {
		return "", fmt.Errorf("empty listenKey in response: %s", string(body))
	}

	return resp.ListenKey, nil
}

// keepaliveListenKey sends keepalive requests every 30 minutes to keep the listenKey active
func (c *AsterClient) keepaliveListenKey(ctx context.Context, listenKey string) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := c.doSignedRequest("PUT", "/fapi/v3/listenKey", url.Values{})
			if err != nil {
				log.Printf("[%s] USER DATA: keepalive failed: %v", c.name, err)
			} else {
				log.Printf("[%s] USER DATA: keepalive sent for listenKey=%s", c.name, listenKey)
			}
		}
	}
}

// handleUserDataStream manages the WebSocket connection with auto-reconnect
func (c *AsterClient) handleUserDataStream(ctx context.Context, listenKey string, callbacks UserDataCallbacks) {
	wsURL := fmt.Sprintf("wss://fstream.asterdex.com/ws/%s", listenKey)
	maxRetries := 10
	retryDelay := 3 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[%s] USER DATA: connecting to %s (attempt %d/%d)", c.name, wsURL, retry+1, maxRetries)

		conn, _, err := c.wsDialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("[%s] USER DATA: dial error (retry %d): %v", c.name, retry, err)
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		log.Printf("[%s] USER DATA: connected to %s", c.name, wsURL)

		// Read messages until disconnect
		disconnected := c.readUserDataMessages(ctx, conn, callbacks)
		conn.Close()

		if !disconnected {
			// Context cancelled, exit gracefully
			return
		}

		// Connection lost, retry
		log.Printf("[%s] USER DATA: disconnected, retrying...", c.name)
		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	log.Printf("[%s] USER DATA: max retries reached, giving up", c.name)
}

// readUserDataMessages reads and parses User Data Stream messages
func (c *AsterClient) readUserDataMessages(ctx context.Context, conn *websocket.Conn, callbacks UserDataCallbacks) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[%s] USER DATA: read error: %v", c.name, err)
			return true // disconnected
		}

		// Parse event type using raw map (case-sensitive)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			log.Printf("[%s] USER DATA: parse error: %v, msg=%s", c.name, err, string(message))
			continue
		}

		var eventType string
		if e, ok := raw["e"]; ok {
			json.Unmarshal(e, &eventType)
		}

		switch eventType {
		case "ORDER_TRADE_UPDATE":
			if callbacks.OnOrderUpdate != nil {
				update := c.parseOrderUpdate(raw)
				if update != nil {
					callbacks.OnOrderUpdate(*update)
				}
			}

		case "ACCOUNT_UPDATE":
			if callbacks.OnAccountUpdate != nil {
				updates := c.parseAccountUpdate(raw)
				for _, update := range updates {
					callbacks.OnAccountUpdate(update)
				}
			}

		default:
			// Ignore unknown event types (listenKeyExpired, etc.)
			if eventType != "" {
				log.Printf("[%s] USER DATA: unknown event type: %s", c.name, eventType)
			}
		}
	}
}

// parseOrderUpdate parses ORDER_TRADE_UPDATE event (Binance-like format)
func (c *AsterClient) parseOrderUpdate(raw map[string]json.RawMessage) *model.OrderUpdate {
	// ORDER_TRADE_UPDATE format:
	// {
	//   "e": "ORDER_TRADE_UPDATE",
	//   "T": 1234567890,
	//   "o": {
	//     "s": "BTCUSDT",
	//     "c": "client_order_id",
	//     "S": "BUY",
	//     "o": "MARKET",
	//     "i": 12345,
	//     "X": "FILLED",
	//     "x": "TRADE",
	//     "z": "0.001",
	//     "ap": "50000.0"
	//   }
	// }

	var timestamp int64
	if t, ok := raw["T"]; ok {
		json.Unmarshal(t, &timestamp)
	}

	var orderRaw map[string]json.RawMessage
	if o, ok := raw["o"]; ok {
		json.Unmarshal(o, &orderRaw)
	} else {
		return nil
	}

	var symbol, clientOrderID, side, status, execType string
	var orderID int64
	var filledQtyStr, avgPriceStr string

	if v, ok := orderRaw["s"]; ok {
		json.Unmarshal(v, &symbol)
	}
	if v, ok := orderRaw["c"]; ok {
		json.Unmarshal(v, &clientOrderID)
	}
	if v, ok := orderRaw["S"]; ok {
		json.Unmarshal(v, &side)
	}
	if v, ok := orderRaw["i"]; ok {
		json.Unmarshal(v, &orderID)
	}
	if v, ok := orderRaw["X"]; ok {
		json.Unmarshal(v, &status)
	}
	if v, ok := orderRaw["x"]; ok {
		json.Unmarshal(v, &execType)
	}
	if v, ok := orderRaw["z"]; ok {
		json.Unmarshal(v, &filledQtyStr)
	}
	if v, ok := orderRaw["ap"]; ok {
		json.Unmarshal(v, &avgPriceStr)
	}

	filledQty, _ := strconv.ParseFloat(filledQtyStr, 64)
	avgPrice, _ := strconv.ParseFloat(avgPriceStr, 64)

	update := &model.OrderUpdate{
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

	log.Printf("[%s] USER DATA ORDER: %s %s %s qty=%.8f price=%.8f orderId=%s clientOrderId=%s status=%s execType=%s",
		c.name, symbol, side, status, filledQty, avgPrice, update.OrderID, clientOrderID, status, execType)

	return update
}

// parseAccountUpdate parses ACCOUNT_UPDATE event (Binance-like format)
func (c *AsterClient) parseAccountUpdate(raw map[string]json.RawMessage) []model.AccountUpdate {
	// ACCOUNT_UPDATE format:
	// {
	//   "e": "ACCOUNT_UPDATE",
	//   "T": 1234567890,
	//   "a": {
	//     "m": "ORDER",
	//     "P": [
	//       {
	//         "s": "BTCUSDT",
	//         "pa": "0.001",
	//         "ps": "LONG"
	//       }
	//     ]
	//   }
	// }

	var timestamp int64
	if t, ok := raw["T"]; ok {
		json.Unmarshal(t, &timestamp)
	}

	var accountRaw map[string]json.RawMessage
	if a, ok := raw["a"]; ok {
		json.Unmarshal(a, &accountRaw)
	} else {
		return nil
	}

	var reason string
	if m, ok := accountRaw["m"]; ok {
		json.Unmarshal(m, &reason)
	}

	var positions []map[string]json.RawMessage
	if p, ok := accountRaw["P"]; ok {
		json.Unmarshal(p, &positions)
	}

	var updates []model.AccountUpdate
	for _, pos := range positions {
		var symbol, side, sizeStr string

		if v, ok := pos["s"]; ok {
			json.Unmarshal(v, &symbol)
		}
		if v, ok := pos["ps"]; ok {
			json.Unmarshal(v, &side)
		}
		if v, ok := pos["pa"]; ok {
			json.Unmarshal(v, &sizeStr)
		}

		size, _ := strconv.ParseFloat(sizeStr, 64)
		if size < 0 {
			size = -size
		}

		update := model.AccountUpdate{
			Exchange:  c.name,
			Symbol:    symbol,
			Side:      side,
			Size:      size,
			Reason:    reason,
			Timestamp: time.UnixMilli(timestamp),
		}

		log.Printf("[%s] USER DATA ACCOUNT: %s side=%s size=%.8f reason=%s",
			c.name, symbol, side, size, reason)

		updates = append(updates, update)
	}

	return updates
}
