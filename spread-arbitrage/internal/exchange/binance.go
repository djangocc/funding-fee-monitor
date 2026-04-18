package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"spread-arbitrage/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// BinanceEndpoints configures REST API paths (differs between normal and unified account)
type BinanceEndpoints struct {
	Order       string // POST: place order
	Position    string // GET: position risk
	AllOrders   string // GET: all orders (委托记录)
	FundingRate string // GET: funding rate / mark price (public, always fapi)
}

// BinanceClient implements the Client interface for Binance Futures and Binance-like exchanges
type BinanceClient struct {
	name         string
	apiKey       string
	apiSecret    string
	baseURL      string
	wsURLTmpl    string
	depthURLTmpl string
	endpoints    BinanceEndpoints
	httpClient   *http.Client
	wsDialer     *websocket.Dialer
}

// Normal (non-unified) Binance Futures endpoints
var BinanceNormalEndpoints = BinanceEndpoints{
	Order:       "/fapi/v1/order",
	Position:    "/fapi/v2/positionRisk",
	AllOrders:   "/fapi/v1/allOrders",
	FundingRate: "/fapi/v1/premiumIndex",
}

// Unified Account (Portfolio Margin) endpoints
var BinanceUnifiedEndpoints = BinanceEndpoints{
	Order:       "/papi/v1/um/order",
	Position:    "/papi/v1/um/positionRisk",
	AllOrders:   "/papi/v1/um/allOrders",
	FundingRate: "/fapi/v1/premiumIndex",
}

// NewBinanceClient creates a client for Binance Futures (normal account)
func NewBinanceClient(apiKey, apiSecret string, unified bool) *BinanceClient {
	eps := BinanceNormalEndpoints
	baseURL := "https://fapi.binance.com"
	if unified {
		eps = BinanceUnifiedEndpoints
		baseURL = "https://papi.binance.com"
	}
	return newBinanceLikeClient(
		"binance",
		apiKey,
		apiSecret,
		baseURL,
		"wss://fstream.binance.com/ws/%s@bookTicker",
		"wss://fstream.binance.com/ws/%s@depth5@100ms",
		eps,
		false,
	)
}

// newBinanceLikeClient creates a generic Binance-compatible client
func newBinanceLikeClient(name, apiKey, apiSecret, baseURL, wsURLTemplate, depthURLTemplate string, endpoints BinanceEndpoints, tlsSkipVerify bool) *BinanceClient {
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	wsDialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	if tlsSkipVerify {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		wsDialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &BinanceClient{
		name:         name,
		apiKey:       apiKey,
		apiSecret:    apiSecret,
		baseURL:      baseURL,
		wsURLTmpl:    wsURLTemplate,
		depthURLTmpl: depthURLTemplate,
		endpoints:    endpoints,
		httpClient:   httpClient,
		wsDialer:     wsDialer,
	}
}

// Name returns the exchange identifier
func (c *BinanceClient) Name() string {
	return c.name
}

// signRequest signs the query string with HMAC-SHA256
func (c *BinanceClient) signRequest(params url.Values) string {
	// Add timestamp
	params.Set("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	// Sort and build query string
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var queryParts []string
	for _, k := range keys {
		queryParts = append(queryParts, fmt.Sprintf("%s=%s", k, params.Get(k)))
	}
	queryString := strings.Join(queryParts, "&")

	// Compute HMAC-SHA256
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(queryString))
	signature := hex.EncodeToString(h.Sum(nil))

	return queryString + "&signature=" + signature
}

// doRequest performs a signed HTTP request with logging
func (c *BinanceClient) doRequest(method, endpoint string, params url.Values) ([]byte, error) {
	signedQuery := c.signRequest(params)
	reqURL := c.baseURL + endpoint + "?" + signedQuery

	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	if len(c.apiKey) > 8 {
		log.Printf("[%s] REST %s %s using key=%s...%s", c.name, method, endpoint, c.apiKey[:4], c.apiKey[len(c.apiKey)-4:])
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[%s] REST %s %s FAILED after %dms: %v", c.name, method, endpoint, elapsed.Milliseconds(), err)
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[%s] REST %s %s status=%d latency=%dms body=%s", c.name, method, endpoint, resp.StatusCode, elapsed.Milliseconds(), string(body))
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(body))
	}

	log.Printf("[%s] REST %s %s status=200 latency=%dms", c.name, method, endpoint, elapsed.Milliseconds())
	return body, nil
}

// SubscribeBookTicker connects to WebSocket and streams book ticker updates
func (c *BinanceClient) SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error) {
	wsURL := fmt.Sprintf(c.wsURLTmpl, strings.ToLower(symbol))
	ch := make(chan model.BookTicker, 100)

	go c.handleBookTickerWS(ctx, wsURL, symbol, ch)

	return ch, nil
}

// handleBookTickerWS manages WebSocket connection with reconnection logic
func (c *BinanceClient) handleBookTickerWS(ctx context.Context, wsURL, symbol string, ch chan model.BookTicker) {
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

// readBookTickerMessages reads and parses WebSocket messages
// Uses raw JSON map to avoid Go's case-insensitive field matching
// (bookTicker has both "b" (bidPrice) and "B" (bidQty), and Go's
// json.Unmarshal picks "B" over "b" for a field tagged `json:"b"`)
func (c *BinanceClient) readBookTickerMessages(ctx context.Context, conn *websocket.Conn, symbol string, ch chan model.BookTicker) bool {
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

		// Parse as raw map to get exact case-sensitive field names
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			continue
		}

		// Extract case-sensitive fields: "b" (bidPrice), "a" (askPrice), "T" (timestamp)
		var bidStr, askStr string
		var timestamp int64
		if b, ok := raw["b"]; ok {
			json.Unmarshal(b, &bidStr)
		}
		if a, ok := raw["a"]; ok {
			json.Unmarshal(a, &askStr)
		}
		if t, ok := raw["T"]; ok {
			json.Unmarshal(t, &timestamp)
		}

		if bidStr == "" || askStr == "" {
			continue
		}

		bid, err1 := strconv.ParseFloat(bidStr, 64)
		ask, err2 := strconv.ParseFloat(askStr, 64)
		if err1 != nil || err2 != nil {
			continue
		}

		ticker := model.BookTicker{
			Exchange:   c.name,
			Symbol:     symbol,
			Bid:        bid,
			Ask:        ask,
			Timestamp:  time.UnixMilli(timestamp),
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

// SubscribeDepth connects to WebSocket and streams order book depth updates
func (c *BinanceClient) SubscribeDepth(ctx context.Context, symbol string) (<-chan model.OrderBook, error) {
	wsURL := fmt.Sprintf(c.depthURLTmpl, strings.ToLower(symbol))
	log.Printf("[%s] SubscribeDepth connecting to %s", c.name, wsURL)
	ch := make(chan model.OrderBook, 100)

	go c.handleDepthWS(ctx, wsURL, symbol, ch)

	return ch, nil
}

// handleDepthWS manages WebSocket connection with reconnection logic
func (c *BinanceClient) handleDepthWS(ctx context.Context, wsURL, symbol string, ch chan model.OrderBook) {
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
			log.Printf("[%s] depth WS dial error (retry %d): %v", c.name, retry, err)
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return
		}
		log.Printf("[%s] depth WS connected: %s", c.name, wsURL)

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

// readDepthMessages reads and parses WebSocket depth messages.
// Uses raw JSON map to avoid Go's case-insensitive field matching
// (depth messages have both "b"/"a" (arrays) and "E"/"e" which conflict).
func (c *BinanceClient) readDepthMessages(ctx context.Context, conn *websocket.Conn, symbol string, ch chan model.OrderBook) bool {
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

		// Parse as raw map for case-sensitive access
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			continue
		}

		// Extract "b" (bids) and "a" (asks) arrays
		var rawBids, rawAsks [][]string
		if b, ok := raw["b"]; ok {
			json.Unmarshal(b, &rawBids)
		}
		if a, ok := raw["a"]; ok {
			json.Unmarshal(a, &rawAsks)
		}

		if len(rawBids) == 0 && len(rawAsks) == 0 {
			continue
		}

		// Parse bids
		bids := make([]model.OrderBookLevel, 0, len(rawBids))
		for _, b := range rawBids {
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
		asks := make([]model.OrderBookLevel, 0, len(rawAsks))
		for _, a := range rawAsks {
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

// binanceOrderResponse is the response from POST /fapi/v1/order
type binanceOrderResponse struct {
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
}

// PlaceMarketOrder places a market order on Binance Futures
func (c *BinanceClient) PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64, clientOrderID string) (*model.Order, error) {
	log.Printf("[%s] ORDER REQUEST: %s %s qty=%.8f clientOrderId=%s", c.name, side, symbol, quantity, clientOrderID)

	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", side)
	params.Set("type", "MARKET")
	params.Set("quantity", fmt.Sprintf("%.8f", quantity))
	if clientOrderID != "" {
		params.Set("newClientOrderId", clientOrderID)
	}

	body, err := c.doRequest("POST", c.endpoints.Order, params)
	if err != nil {
		log.Printf("[%s] ORDER FAILED: %s %s qty=%.8f err=%v", c.name, side, symbol, quantity, err)
		return nil, fmt.Errorf("place order: %w", err)
	}

	log.Printf("[%s] ORDER RAW RESPONSE: %s", c.name, string(body))

	var resp binanceOrderResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("[%s] ORDER PARSE ERROR: %s %s body=%s", c.name, side, symbol, string(body))
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	avgPrice, _ := strconv.ParseFloat(resp.AvgPrice, 64)
	executedQty, _ := strconv.ParseFloat(resp.ExecutedQty, 64)

	// If avgPrice/executedQty are 0 (some API versions don't return them),
	// fall back to the requested quantity
	if executedQty == 0 {
		executedQty = quantity
	}

	order := &model.Order{
		Exchange:      c.name,
		Symbol:        resp.Symbol,
		Side:          resp.Side,
		Quantity:      executedQty,
		Price:         avgPrice,
		OrderID:       fmt.Sprintf("%d", resp.OrderID),
		ClientOrderID: clientOrderID,
		Status:        resp.Status,
		Timestamp:     time.UnixMilli(resp.UpdateTime),
	}

	log.Printf("[%s] ORDER PLACED: %s %s qty=%.8f avgPrice=%.8f orderId=%s clientOrderId=%s status=%s", c.name, order.Side, order.Symbol, order.Quantity, order.Price, order.OrderID, order.ClientOrderID, order.Status)
	return order, nil
}

// binancePositionRisk is the response from GET /fapi/v2/positionRisk
type binancePositionRisk struct {
	Symbol           string `json:"symbol"`
	PositionAmt      string `json:"positionAmt"`
	EntryPrice       string `json:"entryPrice"`
	UnRealizedProfit string `json:"unRealizedProfit"`
}

// GetPosition retrieves the current position for a symbol
func (c *BinanceClient) GetPosition(ctx context.Context, symbol string) (*model.Position, error) {
	params := url.Values{}
	params.Set("symbol", symbol)

	body, err := c.doRequest("GET", c.endpoints.Position, params)
	if err != nil {
		log.Printf("[%s] GET POSITION FAILED: %s err=%v", c.name, symbol, err)
		return nil, fmt.Errorf("get position: %w", err)
	}

	var positions []binancePositionRisk
	if err := json.Unmarshal(body, &positions); err != nil {
		return nil, fmt.Errorf("parse position response: %w", err)
	}

	// Find the position for this symbol
	for _, p := range positions {
		if p.Symbol == symbol {
			posAmt, _ := strconv.ParseFloat(p.PositionAmt, 64)
			entryPrice, _ := strconv.ParseFloat(p.EntryPrice, 64)
			unrealizedPnL, _ := strconv.ParseFloat(p.UnRealizedProfit, 64)

			side := ""
			size := posAmt
			if posAmt > 0 {
				side = "LONG"
			} else if posAmt < 0 {
				side = "SHORT"
				size = -posAmt
			}

			pos := &model.Position{
				Exchange:      c.name,
				Symbol:        symbol,
				Side:          side,
				Size:          size,
				EntryPrice:    entryPrice,
				UnrealizedPnL: unrealizedPnL,
			}
			log.Printf("[%s] POSITION: %s side=%s size=%.8f entry=%.8f pnl=%.8f", c.name, symbol, side, size, entryPrice, unrealizedPnL)
			return pos, nil
		}
	}

	// No position found
	log.Printf("[%s] POSITION: %s side=NONE size=0", c.name, symbol)
	return &model.Position{
		Exchange: c.name,
		Symbol:   symbol,
		Side:     "",
		Size:     0,
	}, nil
}

// GetOrders retrieves recent orders (委托记录) for a symbol
func (c *BinanceClient) GetOrders(ctx context.Context, symbol string) ([]model.Order, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("limit", "50")

	body, err := c.doRequest("GET", c.endpoints.AllOrders, params)
	if err != nil {
		return nil, fmt.Errorf("get orders: %w", err)
	}

	var rawOrders []binanceOrderResponse
	if err := json.Unmarshal(body, &rawOrders); err != nil {
		return nil, fmt.Errorf("parse orders response: %w", err)
	}

	orders := make([]model.Order, 0, len(rawOrders))
	for _, o := range rawOrders {
		avgPrice, _ := strconv.ParseFloat(o.AvgPrice, 64)
		executedQty, _ := strconv.ParseFloat(o.ExecutedQty, 64)
		origQty, _ := strconv.ParseFloat(o.OrigQty, 64)
		if executedQty == 0 {
			executedQty = origQty
		}

		orders = append(orders, model.Order{
			Exchange:      c.name,
			Symbol:        o.Symbol,
			Side:          o.Side,
			Quantity:      executedQty,
			Price:         avgPrice,
			OrderID:       fmt.Sprintf("%d", o.OrderID),
			ClientOrderID: o.ClientOrderID,
			Status:        o.Status,
			Timestamp:     time.UnixMilli(o.UpdateTime),
		})
	}

	// Sort newest-first
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].Timestamp.After(orders[j].Timestamp)
	})

	return orders, nil
}

// GetFundingRate retrieves the current funding rate for a symbol (public endpoint, no signing needed)
func (c *BinanceClient) GetFundingRate(ctx context.Context, symbol string) (*model.FundingRate, error) {
	// Funding rate is always on fapi (public endpoint), even for unified accounts
	reqURL := "https://fapi.binance.com" + c.endpoints.FundingRate + "?symbol=" + symbol
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(body))
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var rateStr, markStr, indexStr string
	var nextTime int64
	if v, ok := raw["lastFundingRate"]; ok { json.Unmarshal(v, &rateStr) }
	if v, ok := raw["markPrice"]; ok { json.Unmarshal(v, &markStr) }
	if v, ok := raw["indexPrice"]; ok { json.Unmarshal(v, &indexStr) }
	if v, ok := raw["nextFundingTime"]; ok { json.Unmarshal(v, &nextTime) }

	rate, _ := strconv.ParseFloat(rateStr, 64)
	mark, _ := strconv.ParseFloat(markStr, 64)
	index, _ := strconv.ParseFloat(indexStr, 64)

	return &model.FundingRate{
		Exchange:        c.name,
		Symbol:          symbol,
		Rate:            rate,
		NextFundingTime: nextTime,
		MarkPrice:       mark,
		IndexPrice:      index,
	}, nil
}

// Close cleans up resources (no-op for stateless REST client)
func (c *BinanceClient) Close() error {
	return nil
}
