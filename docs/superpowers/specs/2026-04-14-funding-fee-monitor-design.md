# Funding Fee Monitor - Design Spec

## Overview

A lightweight, always-on-top floating window for macOS that displays real-time funding rates for selected cryptocurrency perpetual contracts across multiple exchanges.

## Requirements

- Display current funding rates for configurable trading pairs across configurable exchanges
- Always-on-top floating window
- Auto-refresh every 60 seconds (configurable)
- Configuration via JSON file
- Minimal dependencies

## Technology

- **Python + tkinter** (built-in GUI)
- **requests** (HTTP client, only external dependency)
- **concurrent.futures** (parallel API requests, stdlib)

## Supported Exchanges & API Endpoints

### AsterDEX
- **Endpoint:** `GET https://fapi.asterdex.com/fapi/v1/premiumIndex?symbol={symbol}`
- **Symbol format:** `RAVEUSDT`
- **Rate field:** `lastFundingRate`
- **Next funding time field:** `nextFundingTime` (ms timestamp)

### Binance
- **Endpoint:** `GET https://fapi.binance.com/fapi/v1/premiumIndex?symbol={symbol}`
- **Symbol format:** `RAVEUSDT`
- **Rate field:** `lastFundingRate`
- **Next funding time field:** `nextFundingTime` (ms timestamp)

### OKX
- **Endpoint:** `GET https://www.okx.com/api/v5/public/funding-rate?instId={instId}`
- **Symbol format:** `RAVE-USDT-SWAP` (derived from `RAVEUSDT` -> strip `USDT`, insert dashes, append `-SWAP`)
- **Rate field:** `data[0].fundingRate`
- **Next funding time field:** `data[0].nextFundingTime` (ms timestamp)

## Configuration

`config.json`:
```json
{
  "pairs": [
    {
      "symbol": "RAVEUSDT",
      "exchanges": ["aster", "binance", "okx"]
    }
  ],
  "refresh_interval_seconds": 60
}
```

- `pairs`: list of trading pairs, each with a symbol and list of exchanges
- `symbol`: unified symbol format using USDT suffix (e.g., `RAVEUSDT`)
- `exchanges`: subset of `["aster", "binance", "okx"]`
- `refresh_interval_seconds`: polling interval

## Architecture

Single-file Python script (`monitor.py`) with these components:

### 1. Data Fetching
- One function per exchange: `fetch_aster()`, `fetch_binance()`, `fetch_okx()`
- Each returns `{"rate": float, "next_funding_time": int}` or `{"rate": None, "error": str}` on failure
- `fetch_all_rates(pairs)` uses `ThreadPoolExecutor` to parallelize all requests
- OKX symbol conversion: `RAVEUSDT` -> `RAVE-USDT-SWAP`

### 2. UI (tkinter)
- Small window, always-on-top (`root.attributes('-topmost', True)`)
- Dark background for readability
- Table layout: one section per trading pair, one row per exchange
- Funding rate color: green for positive, red for negative
- Displays percentage format (e.g., `-0.1863%`)
- Shows last update timestamp
- Window is draggable

### 3. Refresh Loop
- `root.after(interval_ms, refresh)` for non-blocking periodic updates
- Fetching runs in a background thread to avoid freezing the UI

## File Structure

```
├── config.json
├── monitor.py
└── requirements.txt
```

## Error Handling

- Network errors per-exchange: show "N/A" for that exchange, don't crash
- Invalid config: print error message and exit
- All exchanges fail: show "Error" status, keep retrying on next interval
