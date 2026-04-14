# Funding Fee Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a floating macOS window that displays real-time funding rates for crypto perpetual contracts across AsterDEX, Binance, and OKX.

**Architecture:** Single Python script (`monitor.py`) with tkinter GUI. Reads config from `config.json`. Fetches rates from exchange APIs in parallel using ThreadPoolExecutor, displays in an always-on-top window, refreshes every 60s.

**Tech Stack:** Python 3, tkinter (stdlib), requests, concurrent.futures (stdlib)

**Spec:** `docs/superpowers/specs/2026-04-14-funding-fee-monitor-design.md`

---

## File Structure

- **Create:** `requirements.txt` — single dependency: `requests`
- **Create:** `config.json` — default configuration with RAVEUSDT on all 3 exchanges
- **Create:** `monitor.py` — main application script

---

### Task 1: Project setup and config

**Files:**
- Create: `requirements.txt`
- Create: `config.json`

- [ ] **Step 1: Create requirements.txt**

```
requests
```

- [ ] **Step 2: Create config.json**

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

- [ ] **Step 3: Commit**

```bash
git add requirements.txt config.json
git commit -m "feat: add project config and requirements"
```

---

### Task 2: Data fetching layer

**Files:**
- Create: `monitor.py` (partial — fetching functions only)

Implement the exchange API functions. Each returns `{"rate": float, "next_funding_time": int}` on success or `{"rate": None, "error": str}` on failure.

- [ ] **Step 1: Write the data fetching code in monitor.py**

```python
import json
import requests
from concurrent.futures import ThreadPoolExecutor

EXCHANGE_URLS = {
    "aster": "https://fapi.asterdex.com/fapi/v1/premiumIndex",
    "binance": "https://fapi.binance.com/fapi/v1/premiumIndex",
}
OKX_URL = "https://www.okx.com/api/v5/public/funding-rate"
REQUEST_TIMEOUT = 10


def symbol_to_okx(symbol: str) -> str:
    """Convert 'RAVEUSDT' -> 'RAVE-USDT-SWAP'."""
    base = symbol.replace("USDT", "")
    return f"{base}-USDT-SWAP"


def fetch_binance_like(exchange: str, symbol: str) -> dict:
    """Fetch funding rate from Binance-compatible API (Aster, Binance)."""
    try:
        url = EXCHANGE_URLS[exchange]
        resp = requests.get(url, params={"symbol": symbol}, timeout=REQUEST_TIMEOUT)
        resp.raise_for_status()
        data = resp.json()
        return {
            "rate": float(data["lastFundingRate"]),
            "next_funding_time": int(data["nextFundingTime"]),
        }
    except Exception as e:
        return {"rate": None, "error": str(e)}


def fetch_okx(symbol: str) -> dict:
    """Fetch funding rate from OKX."""
    try:
        inst_id = symbol_to_okx(symbol)
        resp = requests.get(OKX_URL, params={"instId": inst_id}, timeout=REQUEST_TIMEOUT)
        resp.raise_for_status()
        data = resp.json()["data"][0]
        return {
            "rate": float(data["fundingRate"]),
            "next_funding_time": int(data["nextFundingTime"]),
        }
    except Exception as e:
        return {"rate": None, "error": str(e)}


def fetch_rate(exchange: str, symbol: str) -> dict:
    """Dispatch to the correct fetcher."""
    if exchange == "okx":
        return fetch_okx(symbol)
    return fetch_binance_like(exchange, symbol)


def fetch_all_rates(pairs: list) -> dict:
    """Fetch rates for all pairs/exchanges in parallel.

    Returns: {symbol: {exchange: {"rate": float|None, ...}}}
    """
    tasks = []
    for pair in pairs:
        symbol = pair["symbol"]
        for exchange in pair["exchanges"]:
            tasks.append((symbol, exchange))

    results = {}
    with ThreadPoolExecutor(max_workers=len(tasks)) as pool:
        futures = {
            pool.submit(fetch_rate, ex, sym): (sym, ex) for sym, ex in tasks
        }
        for future in futures:
            sym, ex = futures[future]
            results.setdefault(sym, {})[ex] = future.result()
    return results
```

- [ ] **Step 2: Verify fetching works**

Add a temporary `if __name__` block at the end and run it:

```python
if __name__ == "__main__":
    test_pairs = [{"symbol": "RAVEUSDT", "exchanges": ["aster", "binance", "okx"]}]
    results = fetch_all_rates(test_pairs)
    for sym, exchanges in results.items():
        print(f"\n{sym}:")
        for ex, data in exchanges.items():
            if data["rate"] is not None:
                print(f"  {ex}: {data['rate']*100:.4f}%")
            else:
                print(f"  {ex}: ERROR - {data['error']}")
```

Run: `python monitor.py`
Expected: funding rates printed for each exchange (or error messages for unreachable ones).

- [ ] **Step 3: Commit**

```bash
git add monitor.py
git commit -m "feat: add exchange API fetching layer"
```

---

### Task 3: tkinter GUI and refresh loop

**Files:**
- Modify: `monitor.py` — replace the `if __name__` block with GUI code

- [ ] **Step 1: Add the GUI code**

Replace the temporary `if __name__` block with the full tkinter application:

```python
import tkinter as tk
from datetime import datetime
import threading


def load_config(path="config.json") -> dict:
    with open(path) as f:
        return json.load(f)


class FundingMonitor:
    BG = "#1e1e1e"
    FG = "#cccccc"
    GREEN = "#4ec9b0"
    RED = "#f44747"
    HEADER_FG = "#ffffff"

    def __init__(self, config: dict):
        self.config = config
        self.pairs = config["pairs"]
        self.interval = config.get("refresh_interval_seconds", 60) * 1000

        self.root = tk.Tk()
        self.root.title("Funding Rate")
        self.root.configure(bg=self.BG)
        self.root.attributes("-topmost", True)
        self.root.resizable(False, False)

        # Draggable window
        self.root.bind("<Button-1>", self._start_drag)
        self.root.bind("<B1-Motion>", self._on_drag)

        self.labels = {}
        self._build_ui()
        self._refresh()

    def _start_drag(self, event):
        self._drag_x = event.x
        self._drag_y = event.y

    def _on_drag(self, event):
        x = self.root.winfo_x() + event.x - self._drag_x
        y = self.root.winfo_y() + event.y - self._drag_y
        self.root.geometry(f"+{x}+{y}")

    def _build_ui(self):
        pad = {"padx": 8, "pady": 2}

        title = tk.Label(
            self.root, text="Funding Rates", font=("Menlo", 13, "bold"),
            bg=self.BG, fg=self.HEADER_FG,
        )
        title.pack(pady=(8, 4))

        for pair in self.pairs:
            sym = pair["symbol"]

            sym_label = tk.Label(
                self.root, text=sym, font=("Menlo", 11, "bold"),
                bg=self.BG, fg=self.HEADER_FG,
            )
            sym_label.pack(anchor="w", **pad)

            for ex in pair["exchanges"]:
                frame = tk.Frame(self.root, bg=self.BG)
                frame.pack(anchor="w", padx=16, pady=1)

                name_label = tk.Label(
                    frame, text=f"{ex}:", font=("Menlo", 11),
                    bg=self.BG, fg=self.FG, width=10, anchor="w",
                )
                name_label.pack(side="left")

                rate_label = tk.Label(
                    frame, text="loading...", font=("Menlo", 11),
                    bg=self.BG, fg=self.FG, anchor="e", width=12,
                )
                rate_label.pack(side="left")

                self.labels[(sym, ex)] = rate_label

        self.status_label = tk.Label(
            self.root, text="", font=("Menlo", 9),
            bg=self.BG, fg="#666666",
        )
        self.status_label.pack(pady=(4, 8))

    def _refresh(self):
        def do_fetch():
            results = fetch_all_rates(self.pairs)
            self.root.after(0, lambda: self._update_ui(results))

        threading.Thread(target=do_fetch, daemon=True).start()
        self.root.after(self.interval, self._refresh)

    def _update_ui(self, results: dict):
        for (sym, ex), label in self.labels.items():
            data = results.get(sym, {}).get(ex, {"rate": None})
            rate = data.get("rate")
            if rate is not None:
                pct = rate * 100
                label.config(
                    text=f"{pct:+.4f}%",
                    fg=self.GREEN if rate >= 0 else self.RED,
                )
            else:
                label.config(text="N/A", fg="#888888")

        now = datetime.now().strftime("%H:%M:%S")
        self.status_label.config(text=f"Updated: {now}")

    def run(self):
        self.root.mainloop()


if __name__ == "__main__":
    cfg = load_config()
    app = FundingMonitor(cfg)
    app.run()
```

- [ ] **Step 2: Install dependencies and run**

```bash
pip install -r requirements.txt
python monitor.py
```

Expected: a small dark floating window appears showing RAVEUSDT funding rates for all 3 exchanges, always on top, refreshing every 60s.

- [ ] **Step 3: Commit**

```bash
git add monitor.py
git commit -m "feat: add tkinter GUI with auto-refresh"
```

---

## Done

After Task 3, the monitor is fully functional. Run with `python monitor.py`.
