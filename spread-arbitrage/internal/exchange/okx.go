package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
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

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[%s] REST %s %s FAILED after %dms: %v", c.name, method, endpoint, elapsed.Milliseconds(), err)
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[%s] REST %s %s status=%d latency=%dms body=%s", c.name, method, endpoint, resp.StatusCode, elapsed.Milliseconds(), string(respBody))
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[%s] REST %s %s status=200 latency=%dms", c.name, method, endpoint, elapsed.Milliseconds())
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

// okxDepthData is the WebSocket message format for depth
type okxDepthData struct {
	Asks [][]string `json:"asks"` // [[price, quantity, deprecated, orderCount], ...]
	Bids [][]string `json:"bids"` // [[price, quantity, deprecated, orderCount], ...]
}

type okxDepthMsg struct {
	Arg struct {
		Channel string `json:"channel"`
		InstID  string `json:"instId"`
	} `json:"arg"`
	Data []okxDepthData `json:"data"`
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

// SubscribeDepth connects to OKX WebSocket and streams order book depth updates
func (c *OKXClient) SubscribeDepth(ctx context.Context, symbol string) (<-chan model.OrderBook, error) {
	wsURL := "wss://ws.okx.com:8443/ws/v5/public"
	instID := symbolToInstID(symbol)
	ch := make(chan model.OrderBook, 100)

	go c.handleDepthWS(ctx, wsURL, symbol, instID, ch)

	return ch, nil
}

// handleDepthWS manages WebSocket connection with reconnection logic
func (c *OKXClient) handleDepthWS(ctx context.Context, wsURL, symbol, instID string, ch chan model.OrderBook) {
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

		// Subscribe to books5 channel
		subMsg := map[string]interface{}{
			"op": "subscribe",
			"args": []map[string]string{
				{
					"channel": "books5",
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
		disconnected := c.readDepthMessages(ctx, conn, symbol, ch)
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

// readDepthMessages reads and parses OKX WebSocket depth messages
func (c *OKXClient) readDepthMessages(ctx context.Context, conn *websocket.Conn, symbol string, ch chan model.OrderBook) bool {
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

		var msg okxDepthMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			continue // skip malformed messages
		}

		// Process depth data
		for _, data := range msg.Data {
			// Parse bids
			bids := make([]model.OrderBookLevel, 0, len(data.Bids))
			for _, b := range data.Bids {
				if len(b) < 2 {
					continue
				}
				price, err1 := strconv.ParseFloat(b[0], 64)
				qty, err2 := strconv.ParseFloat(b[1], 64)
				if err1 != nil || err2 != nil {
					continue
				}
				bids = append(bids, model.OrderBookLevel{
					Price:    price,
					Quantity: qty,
				})
			}

			// Parse asks
			asks := make([]model.OrderBookLevel, 0, len(data.Asks))
			for _, a := range data.Asks {
				if len(a) < 2 {
					continue
				}
				price, err1 := strconv.ParseFloat(a[0], 64)
				qty, err2 := strconv.ParseFloat(a[1], 64)
				if err1 != nil || err2 != nil {
					continue
				}
				asks = append(asks, model.OrderBookLevel{
					Price:    price,
					Quantity: qty,
				})
			}

			orderBook := model.OrderBook{
				Exchange: c.name,
				Symbol:   symbol,
				Bids:     bids,
				Asks:     asks,
			}

			select {
			case ch <- orderBook:
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
func (c *OKXClient) PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64, clientOrderID string) (*model.Order, error) {
	instID := symbolToInstID(symbol)
	log.Printf("[%s] ORDER REQUEST: %s %s(%s) qty=%.8f clientOrderId=%s", c.name, side, symbol, instID, quantity, clientOrderID)

	orderReq := map[string]interface{}{
		"instId":  instID,
		"tdMode":  "cross",
		"side":    strings.ToLower(side),
		"sz":      fmt.Sprintf("%.8f", quantity),
		"ordType": "market",
	}
	if clientOrderID != "" {
		orderReq["clOrdId"] = clientOrderID
	}

	bodyBytes, err := json.Marshal(orderReq)
	if err != nil {
		return nil, fmt.Errorf("marshal order request: %w", err)
	}

	respBody, err := c.doRequest("POST", "/api/v5/trade/order", string(bodyBytes))
	if err != nil {
		log.Printf("[%s] ORDER FAILED: %s %s qty=%.8f err=%v", c.name, side, symbol, quantity, err)
		return nil, fmt.Errorf("place order: %w", err)
	}

	var resp okxOrderResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("[%s] ORDER PARSE ERROR: %s %s body=%s", c.name, side, symbol, string(respBody))
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	if len(resp.Data) == 0 {
		log.Printf("[%s] ORDER EMPTY RESPONSE: %s %s", c.name, side, symbol)
		return nil, fmt.Errorf("empty order response")
	}

	orderData := resp.Data[0]
	if orderData.SCode != "0" {
		log.Printf("[%s] ORDER REJECTED: %s %s code=%s msg=%s", c.name, side, symbol, orderData.SCode, orderData.SMsg)
		return nil, fmt.Errorf("order failed: %s", orderData.SMsg)
	}

	order := &model.Order{
		Exchange:      c.name,
		Symbol:        symbol,
		Side:          strings.ToUpper(side),
		Quantity:      quantity,
		Price:         0,
		OrderID:       orderData.OrdID,
		ClientOrderID: clientOrderID,
		Status:        "NEW",
		Timestamp:     time.Now(),
	}

	log.Printf("[%s] ORDER FILLED: %s %s qty=%.8f orderId=%s clientOrderId=%s", c.name, order.Side, order.Symbol, order.Quantity, order.OrderID, order.ClientOrderID)
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

	if len(resp.Data) == 0 {
		log.Printf("[%s] POSITION: %s side=NONE size=0", c.name, symbol)
		return &model.Position{
			Exchange: c.name,
			Symbol:   symbol,
			Side:     "",
			Size:     0,
		}, nil
	}

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

		if p.PosSide == "long" && size == 0 {
		} else if p.PosSide == "short" && size == 0 {
		}

		log.Printf("[%s] POSITION: %s side=%s size=%.8f entry=%.8f pnl=%.8f", c.name, symbol, side, size, avgPx, upl)
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
func (c *OKXClient) GetOrders(ctx context.Context, symbol string) ([]model.Order, error) {
	instID := symbolToInstID(symbol)
	endpoint := fmt.Sprintf("/api/v5/trade/orders-history?instType=SWAP&instId=%s&limit=50", instID)

	respBody, err := c.doRequest("GET", endpoint, "")
	if err != nil {
		return nil, fmt.Errorf("get orders: %w", err)
	}

	var resp struct {
		Data []struct {
			InstID  string `json:"instId"`
			OrdID   string `json:"ordId"`
			ClOrdID string `json:"clOrdId"`
			Side    string `json:"side"`
			Sz      string `json:"sz"`
			AvgPx   string `json:"avgPx"`
			State   string `json:"state"`
			UTime   string `json:"uTime"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse orders response: %w", err)
	}

	orders := make([]model.Order, 0, len(resp.Data))
	for _, o := range resp.Data {
		sz, _ := strconv.ParseFloat(o.Sz, 64)
		avgPx, _ := strconv.ParseFloat(o.AvgPx, 64)
		ts, _ := strconv.ParseInt(o.UTime, 10, 64)

		orders = append(orders, model.Order{
			Exchange:      c.name,
			Symbol:        symbol,
			Side:          strings.ToUpper(o.Side),
			Quantity:      sz,
			Price:         avgPx,
			OrderID:       o.OrdID,
			ClientOrderID: o.ClOrdID,
			Status:        o.State,
			Timestamp:     time.UnixMilli(ts),
		})
	}

	sort.Slice(orders, func(i, j int) bool {
		return orders[i].Timestamp.After(orders[j].Timestamp)
	})

	return orders, nil
}

// Close cleans up resources
// GetFundingRate retrieves the current funding rate (public endpoint)
func (c *OKXClient) GetFundingRate(ctx context.Context, symbol string) (*model.FundingRate, error) {
	instID := symbolToInstID(symbol)
	reqURL := c.baseURL + "/api/v5/public/funding-rate?instId=" + instID
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result struct {
		Data []struct {
			FundingRate     string `json:"fundingRate"`
			FundingTime     string `json:"fundingTime"`
			NextFundingTime string `json:"nextFundingTime"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Data) == 0 {
		return nil, fmt.Errorf("parse funding rate response: %w", err)
	}

	d := result.Data[0]
	rate, _ := strconv.ParseFloat(d.FundingRate, 64)
	nextTime, _ := strconv.ParseInt(d.NextFundingTime, 10, 64)

	// OKX funding-rate endpoint doesn't return mark/index, fetch separately via mark-price
	markReqURL := c.baseURL + "/api/v5/public/mark-price?instType=SWAP&instId=" + instID
	markReq, _ := http.NewRequestWithContext(ctx, "GET", markReqURL, nil)
	var markPrice, indexPrice float64
	if markResp, err := c.httpClient.Do(markReq); err == nil {
		defer markResp.Body.Close()
		markBody, _ := io.ReadAll(markResp.Body)
		var markResult struct {
			Data []struct {
				MarkPx string `json:"markPx"`
			} `json:"data"`
		}
		if json.Unmarshal(markBody, &markResult) == nil && len(markResult.Data) > 0 {
			markPrice, _ = strconv.ParseFloat(markResult.Data[0].MarkPx, 64)
		}
	}

	return &model.FundingRate{
		Exchange:        c.name,
		Symbol:          symbol,
		Rate:            rate,
		NextFundingTime: nextTime,
		MarkPrice:       markPrice,
		IndexPrice:      indexPrice,
	}, nil
}

func (c *OKXClient) Close() error {
	return nil
}
