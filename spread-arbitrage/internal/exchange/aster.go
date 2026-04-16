package exchange

// NewAsterClient creates a client for Aster exchange (Binance-compatible API)
// Aster uses the same API format as Binance but with different endpoints
// and requires TLS verification to be skipped due to self-signed certificates
func NewAsterClient(apiKey, apiSecret string) *BinanceClient {
	return newBinanceLikeClient("aster", apiKey, apiSecret,
		"https://fapi.asterdex.com",
		"wss://fstream.asterdex.com/ws/%s@bookTicker",
		"wss://fstream.asterdex.com/ws/%s@depth5@100ms",
		true, // skip TLS verify
	)
}
