package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"spread-arbitrage/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// OKXClient implements the Client interface for OKX futures
type OKXClient struct {
	name       string
	apiKey     string
	apiSecret  string
	passphrase string
	baseURL    string
	httpClient *http.Client
	wsDialer   *websocket.Dialer
}

// NewOKXClient creates a client for OKX futures
func NewOKXClient(apiKey, apiSecret, passphrase string) *OKXClient {
	return &OKXClient{
		name:       "okx",
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		passphrase: passphrase,
		baseURL:    "https://www.okx.com",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		wsDialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		},
	}
}

// Name returns the exchange identifier
func (c *OKXClient) Name() string {
	return c.name
}

// symbolToInstID converts internal symbol format to OKX instrument ID
// "RAVEUSDT" -> "RAVE-USDT-SWAP"
func symbolToInstID(symbol string) string {
	if strings.HasSuffix(symbol, "USDT") {
		base := strings.TrimSuffix(symbol, "USDT")
		return base + "-USDT-SWAP"
	}
	return symbol
}

// signRequest generates OKX signature for REST API
func (c *OKXClient) signRequest(timestamp, method, requestPath, body string) string {
	message := timestamp + method + requestPath + body
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// doRequest performs a signed HTTP request to OKX API
func (c *OKXClient) doRequest(method, endpoint, body string) ([]byte, error) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	requestPath := endpoint
	signature := c.signRequest(timestamp, method, requestPath, body)

	reqURL := c.baseURL + endpoint
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("OK-ACCESS-KEY", c.apiKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// okxTickerData is the WebSocket message format for tickers
type okxTickerData struct {
	InstID string `json:"instId"`
	BidPx  string `json:"bidPx"`
	AskPx  string `json:"askPx"`
	Ts     string `json:"ts"`
}

type okxTickerMsg struct {
	Arg struct {
		Channel string `json:"channel"`
		InstID  string `json:"instId"`
	} `json:"arg"`
	Data []okxTickerData `json:"data"`
}

// SubscribeBookTicker connects to OKX WebSocket and streams book ticker updates
func (c *OKXClient) SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error) {
	wsURL := "wss://ws.okx.com:8443/ws/v5/public"
	instID := symbolToInstID(symbol)
	ch := make(chan model.BookTicker, 100)

	go c.handleBookTickerWS(ctx, wsURL, symbol, instID, ch)

	return ch, nil
}

// handleBookTickerWS manages WebSocket connection with reconnection logic
func (c *OKXClient) handleBookTickerWS(ctx context.Context, wsURL, symbol, instID string, ch chan model.BookTicker) {
	defer close(ch)

	maxRetries := 10
	retryDelay := 3 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, _, err := c.wsDialer.Dial(wsURL, nil)
		if err != nil {
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		// Subscribe to tickers channel
		subMsg := map[string]interface{}{
			"op": "subscribe",
			"args": []map[string]string{
				{
					"channel": "tickers",
					"instId":  instID,
				},
			},
		}

		if err := conn.WriteJSON(subMsg); err != nil {
			conn.Close()
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}

		// Read messages until error or context cancellation
		disconnected := c.readBookTickerMessages(ctx, conn, symbol, ch)
		conn.Close()

		if !disconnected {
			// Context cancelled, exit gracefully
			return
		}

		// Connection lost, retry
		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}
}

// readBookTickerMessages reads and parses OKX WebSocket messages
func (c *OKXClient) readBookTickerMessages(ctx context.Context, conn *websocket.Conn, symbol string, ch chan model.BookTicker) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return true // disconnected
		}

		// Check for ping message
		messageStr := string(message)
		if messageStr == "ping" {
			if err := conn.WriteMessage(websocket.TextMessage, []byte("pong")); err != nil {
				return true // disconnected
			}
			continue
		}

		var msg okxTickerMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			continue // skip malformed messages
		}

		// Process ticker data
		for _, data := range msg.Data {
			bid, _ := strconv.ParseFloat(data.BidPx, 64)
			ask, _ := strconv.ParseFloat(data.AskPx, 64)
			tsMs, _ := strconv.ParseInt(data.Ts, 10, 64)

			ticker := model.BookTicker{
				Exchange:   c.name,
				Symbol:     symbol,
				Bid:        bid,
				Ask:        ask,
				Timestamp:  time.UnixMilli(tsMs),
				ReceivedAt: time.Now(),
			}

			select {
			case ch <- ticker:
			case <-ctx.Done():
				return false
			default:
				// Channel full, skip
			}
		}
	}
}

// okxOrderResponse is the response from POST /api/v5/trade/order
type okxOrderResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		OrdID string `json:"ordId"`
		SCode string `json:"sCode"`
		SMsg  string `json:"sMsg"`
	} `json:"data"`
}

// PlaceMarketOrder places a market order on OKX
func (c *OKXClient) PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64) (*model.Order, error) {
	instID := symbolToInstID(symbol)

	orderReq := map[string]interface{}{
		"instId":  instID,
		"tdMode":  "cross",
		"side":    strings.ToLower(side),
		"sz":      fmt.Sprintf("%.8f", quantity),
		"ordType": "market",
	}

	bodyBytes, err := json.Marshal(orderReq)
	if err != nil {
		return nil, fmt.Errorf("marshal order request: %w", err)
	}

	respBody, err := c.doRequest("POST", "/api/v5/trade/order", string(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("place order: %w", err)
	}

	var resp okxOrderResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("empty order response")
	}

	orderData := resp.Data[0]
	if orderData.SCode != "0" {
		return nil, fmt.Errorf("order failed: %s", orderData.SMsg)
	}

	order := &model.Order{
		Exchange:  c.name,
		Symbol:    symbol,
		Side:      strings.ToUpper(side),
		Quantity:  quantity,
		Price:     0, // OKX market order response doesn't include fill price
		OrderID:   orderData.OrdID,
		Timestamp: time.Now(),
	}

	return order, nil
}

// okxPositionData is the response from GET /api/v5/account/positions
type okxPositionData struct {
	Pos     string `json:"pos"`
	PosSide string `json:"posSide"`
	AvgPx   string `json:"avgPx"`
	Upl     string `json:"upl"`
	InstID  string `json:"instId"`
}

type okxPositionResponse struct {
	Code string            `json:"code"`
	Msg  string            `json:"msg"`
	Data []okxPositionData `json:"data"`
}

// GetPosition retrieves the current position for a symbol
func (c *OKXClient) GetPosition(ctx context.Context, symbol string) (*model.Position, error) {
	instID := symbolToInstID(symbol)
	endpoint := fmt.Sprintf("/api/v5/account/positions?instId=%s", instID)

	respBody, err := c.doRequest("GET", endpoint, "")
	if err != nil {
		return nil, fmt.Errorf("get position: %w", err)
	}

	var resp okxPositionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse position response: %w", err)
	}

	// If no positions, return empty position
	if len(resp.Data) == 0 {
		return &model.Position{
			Exchange: c.name,
			Symbol:   symbol,
			Side:     "",
			Size:     0,
		}, nil
	}

	// Process first matching position
	for _, p := range resp.Data {
		pos, _ := strconv.ParseFloat(p.Pos, 64)
		avgPx, _ := strconv.ParseFloat(p.AvgPx, 64)
		upl, _ := strconv.ParseFloat(p.Upl, 64)

		side := ""
		size := pos
		if pos > 0 {
			side = "LONG"
		} else if pos < 0 {
			side = "SHORT"
			size = -pos
		}

		// Also check posSide field
		if p.PosSide == "long" && size == 0 {
			// Empty long position
		} else if p.PosSide == "short" && size == 0 {
			// Empty short position
		}

		return &model.Position{
			Exchange:      c.name,
			Symbol:        symbol,
			Side:          side,
			Size:          size,
			EntryPrice:    avgPx,
			UnrealizedPnL: upl,
		}, nil
	}

	// No position found, return empty position
	return &model.Position{
		Exchange: c.name,
		Symbol:   symbol,
		Side:     "",
		Size:     0,
	}, nil
}

// okxTradeData is the response from GET /api/v5/trade/fills
type okxTradeData struct {
	InstID string `json:"instId"`
	Side   string `json:"side"`
	Sz     string `json:"sz"`
	FillPx string `json:"fillPx"`
	Fee    string `json:"fee"`
	Ts     string `json:"ts"`
	OrdID  string `json:"ordId"`
}

type okxTradeResponse struct {
	Code string         `json:"code"`
	Msg  string         `json:"msg"`
	Data []okxTradeData `json:"data"`
}

// GetTrades retrieves recent trades for a symbol
func (c *OKXClient) GetTrades(ctx context.Context, symbol string) ([]model.Trade, error) {
	instID := symbolToInstID(symbol)
	endpoint := fmt.Sprintf("/api/v5/trade/fills?instType=SWAP&instId=%s", instID)

	respBody, err := c.doRequest("GET", endpoint, "")
	if err != nil {
		return nil, fmt.Errorf("get trades: %w", err)
	}

	var resp okxTradeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse trades response: %w", err)
	}

	trades := make([]model.Trade, 0, len(resp.Data))
	for _, t := range resp.Data {
		sz, _ := strconv.ParseFloat(t.Sz, 64)
		fillPx, _ := strconv.ParseFloat(t.FillPx, 64)
		fee, _ := strconv.ParseFloat(t.Fee, 64)
		ts, _ := strconv.ParseInt(t.Ts, 10, 64)

		trades = append(trades, model.Trade{
			Exchange:  c.name,
			Symbol:    symbol,
			Side:      strings.ToUpper(t.Side),
			Quantity:  sz,
			Price:     fillPx,
			Fee:       fee,
			Timestamp: time.UnixMilli(ts),
			OrderID:   t.OrdID,
		})
	}

	return trades, nil
}

// Close cleans up resources
func (c *OKXClient) Close() error {
	return nil
}
