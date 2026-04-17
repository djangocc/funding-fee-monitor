[中文版](README_CN.md)

# Spread Arbitrage

Cross-exchange perpetual futures spread convergence arbitrage system. Monitors price differences of the same contract across exchanges and automatically opens/closes positions when spread conditions are met.

## Supported Exchanges

- **Binance** — Standard account (fapi) or Unified/Portfolio Margin account (papi)
- **Aster** — v3 API with EIP-712 wallet signing
- **OKX** — HMAC-SHA256 signing

## Requirements

- Go 1.22+
- Node.js 18+

## Configure API Keys

```bash
cp .env.example .env
```

Edit `.env` (only configure the exchanges you need; unconfigured exchanges will start normally but cannot trade):

```env
# Binance
BINANCE_API_KEY=
BINANCE_API_SECRET=
BINANCE_UNIFIED=true

# Aster (v3 EIP-712)
ASTER_USER=
ASTER_SIGNER=
ASTER_PRIVATE_KEY=

# OKX
OKX_API_KEY=
OKX_API_SECRET=
OKX_PASSPHRASE=

# Server port
PORT=18527
```

### How to Obtain API Keys

#### Binance

1. Log in to https://www.binance.com → Account → API Management
2. Create an API Key, enable "Enable Futures" permission
3. Recommended: bind IP whitelist
4. `BINANCE_UNIFIED`: set to `true` if your account is a Unified/Portfolio Margin account; set to `false` for a standard futures account. You can identify this by the "Portfolio Margin" badge on the Binance web interface.

#### Aster

Aster v3 API uses EIP-712 wallet signing instead of traditional API keys. You need to create an Agent (API wallet) on the Aster platform:

1. Log in to https://app.asterdex.com, switch to **Pro API** mode
2. Create an **Agent** (API wallet); the platform will generate a dedicated signer address
3. Fill in `.env`:
   - `ASTER_USER` — Your main account wallet address (the wallet you log in with)
   - `ASTER_SIGNER` — The API wallet address obtained after creating the Agent
   - `ASTER_PRIVATE_KEY` — The Agent wallet's private key (without `0x` prefix)

API docs: https://asterdex.github.io/aster-api-website/futures-v3/general-info/

#### OKX

1. Log in to https://www.okx.com → API page
2. Create an API Key, set a Passphrase, enable "Trade" permission
3. Recommended: bind IP whitelist
4. Fill in `OKX_API_KEY`, `OKX_API_SECRET`, `OKX_PASSPHRASE`

## Local Development

For local development, run the Go backend and React frontend separately. The Vite dev server automatically proxies `/api` and `/ws` requests to the backend.

**Terminal 1 — Go backend (port 18527):**

```bash
go run .
```

**Terminal 2 — Frontend dev server (port 18528):**

```bash
cd web
npm install   # first time only
npm run dev
```

Open **http://localhost:18528** in your browser. Frontend code changes will hot-reload automatically.

> Note: For local development, open 18528 (frontend), not 18527 (backend). Port 18528 automatically proxies API requests to 18527.

## Server Deployment

On the server, the frontend is compiled to static files and served directly by the Go binary — single process, single port.

### 1. Build Frontend

```bash
cd web
npm install
npm run build
cd ..
```

### 2. Build and Run

```bash
go build -o spread-arbitrage .
./spread-arbitrage
```

Open **http://your-server-ip:18527** in your browser.

### 3. systemd Service (Recommended)

```bash
sudo tee /etc/systemd/system/spread-arbitrage.service > /dev/null <<'EOF'
[Unit]
Description=Spread Arbitrage
After=network.target

[Service]
Type=simple
WorkingDirectory=/path/to/spread-arbitrage
ExecStart=/path/to/spread-arbitrage/spread-arbitrage
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now spread-arbitrage
```

Ensure `.env` and `web/dist/` are both under the `WorkingDirectory`.

```bash
sudo systemctl status spread-arbitrage    # check status
sudo journalctl -u spread-arbitrage -f    # live logs
sudo systemctl restart spread-arbitrage   # restart
```

## Usage

1. Open the Web UI, select a trading pair and two exchanges
2. On the Setup page, click "Test Trade" to verify API connectivity
3. Enter the trading view, create a task and set spread thresholds
4. Start auto trading, or use manual open/close buttons

## Architecture

```
main.go              Entry point, initializes all components
config.go            Loads .env configuration
internal/
  model/types.go     Shared data types
  exchange/          Exchange clients (Binance/Aster/OKX)
  engine/            Trading engine, spread evaluator, task manager
  wsmanager/         WebSocket market data subscription manager
  api/               REST API + WebSocket push
web/                 React frontend (Vite + React)
```
