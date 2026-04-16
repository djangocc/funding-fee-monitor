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

// BinanceClient implements the Client interface for Binance Futures and Binance-like exchanges
type BinanceClient struct {
	name         string
	apiKey       string
	apiSecret    string
	baseURL      string
	wsURLTmpl    string
	depthURLTmpl string
	httpClient   *http.Client
	wsDialer     *websocket.Dialer
}

// NewBinanceClient creates a client for Binance Futures
func NewBinanceClient(apiKey, apiSecret string) *BinanceClient {
	return newBinanceLikeClient(
		"binance",
		apiKey,
		apiSecret,
		"https://fapi.binance.com",
		"wss://fstream.binance.com/ws/%s@bookTicker",
		"wss://fstream.binance.com/ws/%s@depth5@100ms",
		false,
	)
}

// newBinanceLikeClient creates a generic Binance-compatible client
// Allows custom URLs and TLS config for Aster or other exchanges
func newBinanceLikeClient(name, apiKey, apiSecret, baseURL, wsURLTemplate, depthURLTemplate string, tlsSkipVerify bool) *BinanceClient {
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

// doRequest performs a signed HTTP request
func (c *BinanceClient) doRequest(method, endpoint string, params url.Values) ([]byte, error) {
	signedQuery := c.signRequest(params)
	reqURL := c.baseURL + endpoint + "?" + signedQuery

	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)

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

	return body, nil
}

// binanceBookTickerMsg is the WebSocket message format
type binanceBookTickerMsg struct {
	Symbol    string `json:"s"`
	BidPrice  string `json:"b"`
	AskPrice  string `json:"a"`
	Timestamp int64  `json:"T"`
}

// binanceDepthMsg is the WebSocket message format for depth updates
type binanceDepthMsg struct {
	Event  string     `json:"e"`
	Symbol string     `json:"s"`
	Bids   [][]string `json:"b"` // [[price, quantity], ...]
	Asks   [][]string `json:"a"` // [[price, quantity], ...]
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

		var msg binanceBookTickerMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			continue // skip malformed messages
		}

		bid, err1 := strconv.ParseFloat(msg.BidPrice, 64)
		ask, err2 := strconv.ParseFloat(msg.AskPrice, 64)
		if err1 != nil || err2 != nil {
			log.Printf("[%s] invalid price data: bid=%q ask=%q", c.name, msg.BidPrice, msg.AskPrice)
			continue
		}
		if bid <= 0 || ask <= 0 || ask < bid*0.5 {
			log.Printf("[%s] suspicious price: bid=%f ask=%f raw_bid=%q raw_ask=%q", c.name, bid, ask, msg.BidPrice, msg.AskPrice)
		}

		ticker := model.BookTicker{
			Exchange:   c.name,
			Symbol:     symbol,
			Bid:        bid,
			Ask:        ask,
			Timestamp:  time.UnixMilli(msg.Timestamp),
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

// readDepthMessages reads and parses WebSocket depth messages
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

		var msg binanceDepthMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			continue // skip malformed messages
		}

		// Parse bids
		bids := make([]model.OrderBookLevel, 0, len(msg.Bids))
		for _, b := range msg.Bids {
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
		asks := make([]model.OrderBookLevel, 0, len(msg.Asks))
		for _, a := range msg.Asks {
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
	Symbol       string `json:"symbol"`
	OrderID      int64  `json:"orderId"`
	Side         string `json:"side"`
	Type         string `json:"type"`
	OrigQty      string `json:"origQty"`
	ExecutedQty  string `json:"executedQty"`
	AvgPrice     string `json:"avgPrice"`
	UpdateTime   int64  `json:"updateTime"`
}

// PlaceMarketOrder places a market order on Binance Futures
func (c *BinanceClient) PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64) (*model.Order, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", side)
	params.Set("type", "MARKET")
	params.Set("quantity", fmt.Sprintf("%.8f", quantity))

	body, err := c.doRequest("POST", "/fapi/v1/order", params)
	if err != nil {
		return nil, fmt.Errorf("place order: %w", err)
	}

	var resp binanceOrderResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	avgPrice, _ := strconv.ParseFloat(resp.AvgPrice, 64)
	executedQty, _ := strconv.ParseFloat(resp.ExecutedQty, 64)

	order := &model.Order{
		Exchange:  c.name,
		Symbol:    resp.Symbol,
		Side:      resp.Side,
		Quantity:  executedQty,
		Price:     avgPrice,
		OrderID:   fmt.Sprintf("%d", resp.OrderID),
		Timestamp: time.UnixMilli(resp.UpdateTime),
	}

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

	body, err := c.doRequest("GET", "/fapi/v2/positionRisk", params)
	if err != nil {
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

			return &model.Position{
				Exchange:      c.name,
				Symbol:        symbol,
				Side:          side,
				Size:          size,
				EntryPrice:    entryPrice,
				UnrealizedPnL: unrealizedPnL,
			}, nil
		}
	}

	// No position found, return empty position
	return &model.Position{
		Exchange: c.name,
		Symbol:   symbol,
		Side:     "",
		Size:     0,
	}, nil
}

// binanceUserTrade is the response from GET /fapi/v1/userTrades
type binanceUserTrade struct {
	Symbol          string `json:"symbol"`
	ID              int64  `json:"id"`
	OrderID         int64  `json:"orderId"`
	Side            string `json:"side"`
	Price           string `json:"price"`
	Qty             string `json:"qty"`
	Commission      string `json:"commission"`
	CommissionAsset string `json:"commissionAsset"`
	Time            int64  `json:"time"`
}

// GetTrades retrieves recent trades for a symbol
func (c *BinanceClient) GetTrades(ctx context.Context, symbol string) ([]model.Trade, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("limit", "50")

	body, err := c.doRequest("GET", "/fapi/v1/userTrades", params)
	if err != nil {
		return nil, fmt.Errorf("get trades: %w", err)
	}

	var binanceTrades []binanceUserTrade
	if err := json.Unmarshal(body, &binanceTrades); err != nil {
		return nil, fmt.Errorf("parse trades response: %w", err)
	}

	trades := make([]model.Trade, 0, len(binanceTrades))
	for _, bt := range binanceTrades {
		price, _ := strconv.ParseFloat(bt.Price, 64)
		qty, _ := strconv.ParseFloat(bt.Qty, 64)
		commission, _ := strconv.ParseFloat(bt.Commission, 64)

		trades = append(trades, model.Trade{
			Exchange:  c.name,
			Symbol:    bt.Symbol,
			Side:      bt.Side,
			Quantity:  qty,
			Price:     price,
			Fee:       commission,
			Timestamp: time.UnixMilli(bt.Time),
			OrderID:   fmt.Sprintf("%d", bt.OrderID),
		})
	}

	return trades, nil
}

// Close cleans up resources (no-op for stateless REST client)
func (c *BinanceClient) Close() error {
	return nil
}
