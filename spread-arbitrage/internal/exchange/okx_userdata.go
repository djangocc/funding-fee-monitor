package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"spread-arbitrage/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// okxSymbolFromInstID converts OKX instrument ID to internal symbol format
// "BTC-USDT-SWAP" -> "BTCUSDT"
func okxSymbolFromInstID(instID string) string {
	// Remove -SWAP suffix
	instID = strings.TrimSuffix(instID, "-SWAP")
	// Remove hyphens: "BTC-USDT" -> "BTCUSDT"
	return strings.ReplaceAll(instID, "-", "")
}

// okxLoginMsg represents the OKX WebSocket login message
type okxLoginMsg struct {
	Op   string `json:"op"`
	Args []struct {
		APIKey     string `json:"apiKey"`
		Passphrase string `json:"passphrase"`
		Timestamp  string `json:"timestamp"`
		Sign       string `json:"sign"`
	} `json:"args"`
}

// okxWSResponse represents generic OKX WebSocket response
type okxWSResponse struct {
	Event string `json:"event,omitempty"`
	Code  string `json:"code,omitempty"`
	Msg   string `json:"msg,omitempty"`
	Arg   struct {
		Channel  string `json:"channel,omitempty"`
		InstType string `json:"instType,omitempty"`
	} `json:"arg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// okxOrderData represents an order update from OKX
type okxOrderData struct {
	InstID    string `json:"instId"`
	OrdID     string `json:"ordId"`
	ClOrdID   string `json:"clOrdId"`
	Side      string `json:"side"`      // buy, sell
	State     string `json:"state"`     // live, partially_filled, filled, canceled
	FillSz    string `json:"fillSz"`    // filled quantity
	AvgPx     string `json:"avgPx"`     // average fill price
	UTime     string `json:"uTime"`     // update time in milliseconds
	ExecType  string `json:"execType"`  // T=trade, M=maker, N=new
}

// okxPositionUpdateData represents a position update from OKX User Data Stream
type okxPositionUpdateData struct {
	InstID  string `json:"instId"`
	Pos     string `json:"pos"`     // position size (can be negative for short)
	PosSide string `json:"posSide"` // long, short, net
	AvgPx   string `json:"avgPx"`   // average entry price
	UTime   string `json:"uTime"`   // update time in milliseconds
}

// SubscribeUserData connects to OKX private WebSocket and streams user data updates
func (c *OKXClient) SubscribeUserData(ctx context.Context, callbacks UserDataCallbacks) error {
	if c.apiKey == "" {
		return fmt.Errorf("API key not configured")
	}

	wsURL := "wss://ws.okx.com:8443/ws/v5/private"

	go c.handleUserDataWS(ctx, wsURL, callbacks)

	return nil
}

// handleUserDataWS manages WebSocket connection with reconnection logic
func (c *OKXClient) handleUserDataWS(ctx context.Context, wsURL string, callbacks UserDataCallbacks) {
	maxRetries := 10
	retryDelay := 3 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		select {
		case <-ctx.Done():
			log.Printf("[%s] USER DATA: context cancelled, exiting", c.name)
			return
		default:
		}

		if retry > 0 {
			log.Printf("[%s] USER DATA: reconnecting (attempt %d/%d)", c.name, retry+1, maxRetries)
		}

		conn, _, err := c.wsDialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("[%s] USER DATA: dial failed: %v", c.name, err)
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		// Login to OKX private WebSocket
		if err := c.loginOKXWS(conn); err != nil {
			log.Printf("[%s] USER DATA: login failed: %v", c.name, err)
			conn.Close()
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		// Subscribe to orders and positions channels
		if err := c.subscribeOKXChannels(conn); err != nil {
			log.Printf("[%s] USER DATA: subscribe failed: %v", c.name, err)
			conn.Close()
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		log.Printf("[%s] USER DATA: connected and subscribed", c.name)

		// Read messages until error or context cancellation
		disconnected := c.readUserDataMessages(ctx, conn, callbacks)
		conn.Close()

		if !disconnected {
			// Context cancelled, exit gracefully
			log.Printf("[%s] USER DATA: context done, exiting gracefully", c.name)
			return
		}

		// Connection lost, retry
		log.Printf("[%s] USER DATA: connection lost", c.name)
		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	log.Printf("[%s] USER DATA: max retries reached, exiting", c.name)
}

// loginOKXWS performs login to OKX private WebSocket
func (c *OKXClient) loginOKXWS(conn *websocket.Conn) error {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sign := c.signRequest(timestamp, "GET", "/users/self/verify", "")

	loginMsg := map[string]interface{}{
		"op": "login",
		"args": []map[string]string{
			{
				"apiKey":     c.apiKey,
				"passphrase": c.passphrase,
				"timestamp":  timestamp,
				"sign":       sign,
			},
		},
	}

	if err := conn.WriteJSON(loginMsg); err != nil {
		return fmt.Errorf("send login message: %w", err)
	}

	// Wait for login response (with timeout)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	_, respMsg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read login response: %w", err)
	}

	var resp okxWSResponse
	if err := json.Unmarshal(respMsg, &resp); err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}

	if resp.Event != "login" || resp.Code != "0" {
		return fmt.Errorf("login failed: code=%s msg=%s", resp.Code, resp.Msg)
	}

	log.Printf("[%s] USER DATA: login successful", c.name)
	return nil
}

// subscribeOKXChannels subscribes to orders and positions channels
func (c *OKXClient) subscribeOKXChannels(conn *websocket.Conn) error {
	subMsg := map[string]interface{}{
		"op": "subscribe",
		"args": []map[string]string{
			{
				"channel":  "orders",
				"instType": "SWAP",
			},
			{
				"channel":  "positions",
				"instType": "SWAP",
			},
		},
	}

	if err := conn.WriteJSON(subMsg); err != nil {
		return fmt.Errorf("send subscribe message: %w", err)
	}

	log.Printf("[%s] USER DATA: subscription sent", c.name)
	return nil
}

// readUserDataMessages reads and processes OKX private WebSocket messages
func (c *OKXClient) readUserDataMessages(ctx context.Context, conn *websocket.Conn, callbacks UserDataCallbacks) bool {
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

		// Check for ping message
		messageStr := string(message)
		if messageStr == "ping" {
			if err := conn.WriteMessage(websocket.TextMessage, []byte("pong")); err != nil {
				log.Printf("[%s] USER DATA: pong write error: %v", c.name, err)
				return true // disconnected
			}
			continue
		}

		var resp okxWSResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			log.Printf("[%s] USER DATA: parse error: %v", c.name, err)
			continue
		}

		// Handle subscription confirmation
		if resp.Event == "subscribe" {
			if resp.Code == "0" {
				log.Printf("[%s] USER DATA: subscribed to channel=%s", c.name, resp.Arg.Channel)
			} else {
				log.Printf("[%s] USER DATA: subscription failed: channel=%s code=%s msg=%s",
					c.name, resp.Arg.Channel, resp.Code, resp.Msg)
			}
			continue
		}

		// Process data updates
		if len(resp.Data) > 0 {
			switch resp.Arg.Channel {
			case "orders":
				c.handleOrderUpdate(resp.Data, callbacks)
			case "positions":
				c.handlePositionUpdate(resp.Data, callbacks)
			}
		}
	}
}

// handleOrderUpdate processes order updates from OKX
func (c *OKXClient) handleOrderUpdate(data json.RawMessage, callbacks UserDataCallbacks) {
	if callbacks.OnOrderUpdate == nil {
		return
	}

	var orders []okxOrderData
	if err := json.Unmarshal(data, &orders); err != nil {
		log.Printf("[%s] USER DATA: parse orders data error: %v", c.name, err)
		return
	}

	for _, order := range orders {
		symbol := okxSymbolFromInstID(order.InstID)
		side := strings.ToUpper(order.Side)
		status := c.mapOKXOrderStatus(order.State)

		filledQty, _ := strconv.ParseFloat(order.FillSz, 64)
		avgPrice, _ := strconv.ParseFloat(order.AvgPx, 64)
		tsMs, _ := strconv.ParseInt(order.UTime, 10, 64)

		// Determine execution type
		execType := c.mapOKXExecType(order.State, order.ExecType)

		update := model.OrderUpdate{
			Exchange:      c.name,
			Symbol:        symbol,
			OrderID:       order.OrdID,
			ClientOrderID: order.ClOrdID,
			Side:          side,
			Status:        status,
			ExecType:      execType,
			FilledQty:     filledQty,
			AvgPrice:      avgPrice,
			Timestamp:     time.UnixMilli(tsMs),
		}

		log.Printf("[%s] USER DATA ORDER: %s %s status=%s filled=%.8f@%.8f orderId=%s clientOrderId=%s",
			c.name, symbol, side, status, filledQty, avgPrice, order.OrdID, order.ClOrdID)

		callbacks.OnOrderUpdate(update)
	}
}

// handlePositionUpdate processes position updates from OKX
func (c *OKXClient) handlePositionUpdate(data json.RawMessage, callbacks UserDataCallbacks) {
	if callbacks.OnAccountUpdate == nil {
		return
	}

	var positions []okxPositionUpdateData
	if err := json.Unmarshal(data, &positions); err != nil {
		log.Printf("[%s] USER DATA: parse positions data error: %v", c.name, err)
		return
	}

	for _, pos := range positions {
		symbol := okxSymbolFromInstID(pos.InstID)
		posSize, _ := strconv.ParseFloat(pos.Pos, 64)
		tsMs, _ := strconv.ParseInt(pos.UTime, 10, 64)

		// Determine side and absolute size
		var side string
		var size float64

		if posSize > 0 {
			side = "LONG"
			size = posSize
		} else if posSize < 0 {
			side = "SHORT"
			size = math.Abs(posSize)
		} else {
			// Zero position - could be from either side closing
			// Use posSide to determine which side was closed
			if pos.PosSide == "long" {
				side = "LONG"
			} else if pos.PosSide == "short" {
				side = "SHORT"
			} else {
				side = "LONG" // default
			}
			size = 0
		}

		update := model.AccountUpdate{
			Exchange:  c.name,
			Symbol:    symbol,
			Side:      side,
			Size:      size,
			Reason:    "ORDER", // OKX doesn't distinguish in positions channel
			Timestamp: time.UnixMilli(tsMs),
		}

		log.Printf("[%s] USER DATA POSITION: %s side=%s size=%.8f",
			c.name, symbol, side, size)

		callbacks.OnAccountUpdate(update)
	}
}

// mapOKXOrderStatus maps OKX order state to standard status
func (c *OKXClient) mapOKXOrderStatus(state string) string {
	switch state {
	case "live":
		return "NEW"
	case "partially_filled":
		return "PARTIALLY_FILLED"
	case "filled":
		return "FILLED"
	case "canceled":
		return "CANCELED"
	case "expired":
		return "EXPIRED"
	default:
		return strings.ToUpper(state)
	}
}

// mapOKXExecType maps OKX order state and execution type to standard exec type
func (c *OKXClient) mapOKXExecType(state, execType string) string {
	switch state {
	case "filled", "partially_filled":
		return "TRADE"
	case "canceled":
		return "CANCELED"
	case "expired":
		return "EXPIRED"
	case "live":
		return "NEW"
	default:
		return "NEW"
	}
}
