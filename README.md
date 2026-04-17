[中文版](README_CN.md)

# Curitiba — Crypto Funding Rate Monitor & Arbitrage System

A suite of independent tools for collecting, monitoring, and trading on perpetual futures funding rates and cross-exchange price spreads.

## Components

| Component | Language | Description |
|-----------|----------|-------------|
| `data-collector/` | Python | Real-time market data collection to InfluxDB |
| `web/` | Python (Flask) | Funding rate visualization dashboard |
| `monitor.py` | Python (Tkinter) | Desktop real-time spread monitor with alerts |
| `spread-arbitrage/` | Go + React | Cross-exchange spread arbitrage trading system |

---

### data-collector — Market Data Collection

Collects real-time market data from multiple exchanges via WebSocket and writes to InfluxDB.

- Data: best bid/ask (bookTicker), index/mark prices, order book depth
- Exchanges: Binance, Aster, OKX
- 100ms aggregation, auto-reconnect
- Hourly sync of leverage, funding rate caps, and other parameters from exchange APIs

```bash
cd data-collector
python collector.py
```

Config: `data-collector/config.json` (InfluxDB connection, trading pairs, exchange parameters)

---

### web — Funding Rate Dashboard

Flask web service that reads historical data from InfluxDB and displays price trends and funding rate charts.

- Contract price, index price, mark price line charts
- Premium% and Funding Rate% dual-axis chart
- Cross-exchange spread candlestick chart
- Multiple time granularities: 1s to 1h

```bash
cd web
python app.py
```

Visit `http://localhost:5000`

---

### monitor.py — Desktop Spread Monitor

Tkinter desktop GUI that displays real-time prices and funding rates, with audio alerts when spread exceeds thresholds.

- WebSocket real-time prices + 15s funding rate polling
- Configurable open/close alert thresholds
- Always-on-top, draggable window, per-exchange/direction mute

```bash
python monitor.py
```

---

### spread-arbitrage — Cross-Exchange Spread Arbitrage

Automated trading system with Go backend + React frontend. See [spread-arbitrage/README.md](spread-arbitrage/README.md) for deployment details.

- Monitors spread between the same contract on two exchanges, auto open/close when conditions are met
- Supports both short-spread and long-spread strategies
- Consecutive confirmation mechanism to prevent false triggers
- Single-leg failure auto-reversal with up to 3 retries
- Web UI: order books, funding rates, positions, order history with paired order spread tooltip

```bash
cd spread-arbitrage
cp .env.example .env   # Configure API keys
cd web && npm install && npm run build && cd ..
go build -o spread-arbitrage .
./spread-arbitrage
```

Visit `http://localhost:18527`

---

## Disclaimer & Security Recommendations

**This project is for educational and research purposes only and does not constitute investment advice. Cryptocurrency trading carries extremely high risks, including but not limited to market volatility, exchange outages, network latency, and software bugs, all of which may result in financial loss. The author assumes no responsibility for any losses incurred from live trading with this system.**

### Recommendations

- **Use a sub-account with small funds for testing.** All exchanges support sub-accounts. Verify strategy logic and system stability with minimal capital before scaling up.
- **Follow the principle of least privilege for API keys.** Only enable necessary permissions (futures trading). Never enable withdrawal permissions. Always bind IP whitelists, restricted to the server running this program.
- **Do not expose the Web UI on public networks.** This system has no authentication. Use SSH tunnels or VPN for remote access.
- **Keep your `.env` file safe.** It contains exchange keys and private keys. Never commit to Git or share with others.
