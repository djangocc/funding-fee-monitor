package exchange

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"net/url"
	"spread-arbitrage/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/gorilla/websocket"
)

// AsterClient implements the Client interface for Aster v3 API (EIP-712 signing)
type AsterClient struct {
	name       string
	user       string // main wallet address
	signer     string // API wallet address
	privateKey *ecdsa.PrivateKey
	baseURL    string
	httpClient *http.Client
	wsDialer   *websocket.Dialer
}

// NewAsterClient creates a client for Aster v3 API
func NewAsterClient(user, signer, privateKeyHex string) *AsterClient {
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:    &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:       100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:    90 * time.Second,
		},
	}
	wsDialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
	}

	var privKey *ecdsa.PrivateKey
	if privateKeyHex != "" {
		// Strip 0x prefix if present
		hex := strings.TrimPrefix(privateKeyHex, "0x")
		var err error
		privKey, err = crypto.HexToECDSA(hex)
		if err != nil {
			log.Printf("[aster] WARNING: invalid private key: %v", err)
		}
	}

	return &AsterClient{
		name:       "aster",
		user:       user,
		signer:     signer,
		privateKey: privKey,
		baseURL:    "https://fapi.asterdex.com",
		httpClient: httpClient,
		wsDialer:   wsDialer,
	}
}

func (c *AsterClient) Name() string { return c.name }

// getNonce returns current time in microseconds (Aster v3 requirement)
func (c *AsterClient) getNonce() string {
	return strconv.FormatInt(time.Now().UnixMicro(), 10)
}

// signEIP712 signs a message string using EIP-712 structured data
func (c *AsterClient) signEIP712(msg string) (string, error) {
	if c.privateKey == nil {
		return "", fmt.Errorf("private key not configured")
	}

	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"Message": {
				{Name: "msg", Type: "string"},
			},
		},
		PrimaryType: "Message",
		Domain: apitypes.TypedDataDomain{
			Name:              "AsterSignTransaction",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(1666),
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Message: apitypes.TypedDataMessage{
			"msg": msg,
		},
	}

	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return "", fmt.Errorf("hash domain: %w", err)
	}

	messageHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return "", fmt.Errorf("hash message: %w", err)
	}

	// EIP-712 hash: keccak256("\x19\x01" + domainSeparator + messageHash)
	rawData := append([]byte{0x19, 0x01}, domainSeparator...)
	rawData = append(rawData, messageHash...)
	hash := crypto.Keccak256(rawData)

	sig, err := crypto.Sign(hash, c.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	// Adjust V value (27/28 for Ethereum)
	if sig[64] < 27 {
		sig[64] += 27
	}

	return fmt.Sprintf("0x%x", sig), nil
}

// doSignedRequest performs a signed v3 API request
func (c *AsterClient) doSignedRequest(method, endpoint string, params url.Values) ([]byte, error) {
	// Add auth params
	params.Set("user", c.user)
	params.Set("signer", c.signer)
	params.Set("nonce", c.getNonce())

	// Build message string for signing (URL-encoded params)
	msg := params.Encode()

	sig, err := c.signEIP712(msg)
	if err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}
	params.Set("signature", sig)

	var reqURL string
	var reqBody io.Reader
	if method == "GET" || method == "DELETE" {
		reqURL = c.baseURL + endpoint + "?" + params.Encode()
	} else {
		reqURL = c.baseURL + endpoint
		reqBody = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequest(method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

// doPublicRequest performs an unsigned public API request
func (c *AsterClient) doPublicRequest(endpoint string, params url.Values) ([]byte, error) {
	reqURL := c.baseURL + endpoint
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[%s] REST GET %s FAILED after %dms: %v", c.name, endpoint, elapsed.Milliseconds(), err)
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[%s] REST GET %s status=%d latency=%dms body=%s", c.name, endpoint, resp.StatusCode, elapsed.Milliseconds(), string(body))
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(body))
	}

	log.Printf("[%s] REST GET %s status=200 latency=%dms", c.name, endpoint, elapsed.Milliseconds())
	return body, nil
}

// ─── WebSocket (same as Binance, format unchanged in v3) ───

func (c *AsterClient) SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error) {
	wsURL := fmt.Sprintf("wss://fstream.asterdex.com/ws/%s@bookTicker", strings.ToLower(symbol))
	ch := make(chan model.BookTicker, 100)

	go func() {
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

			disconnected := readBinanceLikeBookTicker(ctx, conn, c.name, symbol, ch)
			conn.Close()
			if !disconnected {
				return
			}
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
			}
		}
	}()

	return ch, nil
}

func (c *AsterClient) SubscribeDepth(ctx context.Context, symbol string) (<-chan model.OrderBook, error) {
	wsURL := fmt.Sprintf("wss://fstream.asterdex.com/ws/%s@depth5@100ms", strings.ToLower(symbol))
	log.Printf("[%s] SubscribeDepth connecting to %s", c.name, wsURL)
	ch := make(chan model.OrderBook, 100)

	go func() {
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

			disconnected := readBinanceLikeDepth(ctx, conn, c.name, symbol, ch)
			conn.Close()
			if !disconnected {
				return
			}
			if retry < maxRetries-1 {
				time.Sleep(retryDelay)
			}
		}
	}()

	return ch, nil
}

// ─── REST: v3 endpoints ───

func (c *AsterClient) PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64, clientOrderID string) (*model.Order, error) {
	log.Printf("[%s] ORDER REQUEST: %s %s qty=%.8f clientOrderId=%s", c.name, side, symbol, quantity, clientOrderID)

	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", side)
	params.Set("type", "MARKET")
	params.Set("quantity", fmt.Sprintf("%.8f", quantity))
	if clientOrderID != "" {
		params.Set("newClientOrderId", clientOrderID)
	}

	body, err := c.doSignedRequest("POST", "/fapi/v3/order", params)
	if err != nil {
		log.Printf("[%s] ORDER FAILED: %s %s qty=%.8f err=%v", c.name, side, symbol, quantity, err)
		return nil, fmt.Errorf("place order: %w", err)
	}

	// Parse response (same format as Binance)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		log.Printf("[%s] ORDER PARSE ERROR: %s %s body=%s", c.name, side, symbol, string(body))
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	var orderID int64
	var respSymbol, respSide, avgPriceStr, executedQtyStr, respClientOrderID, respStatus string
	var updateTime int64
	log.Printf("[%s] ORDER RAW RESPONSE: %s", c.name, string(body))

	if v, ok := raw["orderId"]; ok { json.Unmarshal(v, &orderID) }
	if v, ok := raw["symbol"]; ok { json.Unmarshal(v, &respSymbol) }
	if v, ok := raw["side"]; ok { json.Unmarshal(v, &respSide) }
	if v, ok := raw["avgPrice"]; ok { json.Unmarshal(v, &avgPriceStr) }
	if v, ok := raw["executedQty"]; ok { json.Unmarshal(v, &executedQtyStr) }
	if v, ok := raw["updateTime"]; ok { json.Unmarshal(v, &updateTime) }
	if v, ok := raw["clientOrderId"]; ok { json.Unmarshal(v, &respClientOrderID) }
	if v, ok := raw["status"]; ok { json.Unmarshal(v, &respStatus) }

	avgPrice, _ := strconv.ParseFloat(avgPriceStr, 64)
	executedQty, _ := strconv.ParseFloat(executedQtyStr, 64)

	// Fallback: some API versions don't return executedQty in initial response
	if executedQty == 0 {
		executedQty = quantity
	}

	order := &model.Order{
		Exchange:      c.name,
		Symbol:        respSymbol,
		Side:          respSide,
		Quantity:      executedQty,
		Price:         avgPrice,
		OrderID:       fmt.Sprintf("%d", orderID),
		ClientOrderID: respClientOrderID,
		Status:        respStatus,
		Timestamp:     time.UnixMilli(updateTime),
	}

	log.Printf("[%s] ORDER PLACED: %s %s qty=%.8f avgPrice=%.8f orderId=%s clientOrderId=%s status=%s", c.name, order.Side, order.Symbol, order.Quantity, order.Price, order.OrderID, order.ClientOrderID, order.Status)
	return order, nil
}

func (c *AsterClient) GetPosition(ctx context.Context, symbol string) (*model.Position, error) {
	params := url.Values{}
	params.Set("symbol", symbol)

	body, err := c.doSignedRequest("GET", "/fapi/v3/positionRisk", params)
	if err != nil {
		log.Printf("[%s] GET POSITION FAILED: %s err=%v", c.name, symbol, err)
		return nil, fmt.Errorf("get position: %w", err)
	}

	var positions []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		UnRealizedProfit string `json:"unRealizedProfit"`
	}
	if err := json.Unmarshal(body, &positions); err != nil {
		return nil, fmt.Errorf("parse position response: %w", err)
	}

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

			log.Printf("[%s] POSITION: %s side=%s size=%.8f entry=%.8f pnl=%.8f", c.name, symbol, side, size, entryPrice, unrealizedPnL)
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

	log.Printf("[%s] POSITION: %s side=NONE size=0", c.name, symbol)
	return &model.Position{Exchange: c.name, Symbol: symbol}, nil
}

func (c *AsterClient) GetOrders(ctx context.Context, symbol string) ([]model.Order, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("limit", "50")

	body, err := c.doSignedRequest("GET", "/fapi/v3/allOrders", params)
	if err != nil {
		return nil, fmt.Errorf("get orders: %w", err)
	}

	var rawOrders []struct {
		Symbol        string `json:"symbol"`
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Side          string `json:"side"`
		Status        string `json:"status"`
		AvgPrice      string `json:"avgPrice"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		UpdateTime    int64  `json:"updateTime"`
	}
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

	sort.Slice(orders, func(i, j int) bool {
		return orders[i].Timestamp.After(orders[j].Timestamp)
	})

	return orders, nil
}

func (c *AsterClient) GetFundingRate(ctx context.Context, symbol string) (*model.FundingRate, error) {
	params := url.Values{}
	params.Set("symbol", symbol)

	body, err := c.doPublicRequest("/fapi/v3/premiumIndex", params)
	if err != nil {
		return nil, fmt.Errorf("get funding rate: %w", err)
	}

	// Use raw map to handle case-sensitivity
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse funding rate: %w", err)
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

func (c *AsterClient) Close() error { return nil }

// ─── Shared Binance-like WS parsers (used by both Binance and Aster) ───

// readBinanceLikeBookTicker reads bookTicker messages from a Binance-format WS connection.
// Returns true if disconnected (should retry), false if context cancelled.
func readBinanceLikeBookTicker(ctx context.Context, conn *websocket.Conn, name, symbol string, ch chan model.BookTicker) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return true
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			continue
		}

		var bidStr, askStr string
		var timestamp int64
		if b, ok := raw["b"]; ok { json.Unmarshal(b, &bidStr) }
		if a, ok := raw["a"]; ok { json.Unmarshal(a, &askStr) }
		if t, ok := raw["T"]; ok { json.Unmarshal(t, &timestamp) }

		if bidStr == "" || askStr == "" {
			continue
		}

		bid, err1 := strconv.ParseFloat(bidStr, 64)
		ask, err2 := strconv.ParseFloat(askStr, 64)
		if err1 != nil || err2 != nil {
			continue
		}

		ticker := model.BookTicker{
			Exchange:   name,
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
		}
	}
}

// readBinanceLikeDepth reads depth messages from a Binance-format WS connection.
func readBinanceLikeDepth(ctx context.Context, conn *websocket.Conn, name, symbol string, ch chan model.OrderBook) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return true
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			continue
		}

		var rawBids, rawAsks [][]string
		if b, ok := raw["b"]; ok { json.Unmarshal(b, &rawBids) }
		if a, ok := raw["a"]; ok { json.Unmarshal(a, &rawAsks) }

		if len(rawBids) == 0 && len(rawAsks) == 0 {
			continue
		}

		bids := make([]model.OrderBookLevel, 0, len(rawBids))
		for _, b := range rawBids {
			if len(b) < 2 { continue }
			price, err1 := strconv.ParseFloat(b[0], 64)
			qty, err2 := strconv.ParseFloat(b[1], 64)
			if err1 != nil || err2 != nil { continue }
			bids = append(bids, model.OrderBookLevel{Price: price, Quantity: qty})
		}

		asks := make([]model.OrderBookLevel, 0, len(rawAsks))
		for _, a := range rawAsks {
			if len(a) < 2 { continue }
			price, err1 := strconv.ParseFloat(a[0], 64)
			qty, err2 := strconv.ParseFloat(a[1], 64)
			if err1 != nil || err2 != nil { continue }
			asks = append(asks, model.OrderBookLevel{Price: price, Quantity: qty})
		}

		book := model.OrderBook{
			Exchange: name,
			Symbol:   symbol,
			Bids:     bids,
			Asks:     asks,
		}

		select {
		case ch <- book:
		case <-ctx.Done():
			return false
		default:
		}
	}
}
