import json
import os
import ssl
import subprocess
import threading
import tkinter as tk
import traceback
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime

import requests
import websocket

# --- HTTP endpoints (funding rate only) ---
FUNDING_URLS = {
    "aster": "https://fapi.asterdex.com/fapi/v1/premiumIndex",
    "binance": "https://fapi.binance.com/fapi/v1/premiumIndex",
}
OKX_FUNDING_URLS = [
    "https://www.okx.com/api/v5/public/funding-rate",
    "https://www.okx.com/priapi/v5/public/funding-rate",
]
REQUEST_TIMEOUT = 10

# --- WebSocket endpoints (price only) ---
WS_URLS = {
    "aster": "wss://fstream.asterdex.com/ws/{symbol_lower}@bookTicker",
    "binance": "wss://fstream.binance.com/ws/{symbol_lower}@bookTicker",
    "okx": "wss://ws.okx.com:8443/ws/v5/public",
}


def symbol_to_okx(symbol: str) -> str:
    base = symbol.replace("USDT", "")
    return f"{base}-USDT-SWAP"


# --- Funding rate fetchers (HTTP, 15s interval) ---

def _okx_get(urls: list, params: dict) -> requests.Response:
    last_err = None
    for url in urls:
        for verify in (True, False):
            try:
                resp = requests.get(url, params=params, timeout=REQUEST_TIMEOUT, verify=verify)
                resp.raise_for_status()
                return resp
            except Exception as e:
                last_err = e
    raise last_err


def fetch_funding_binance_like(exchange: str, symbol: str) -> dict:
    try:
        url = FUNDING_URLS[exchange]
        resp = requests.get(url, params={"symbol": symbol}, timeout=REQUEST_TIMEOUT)
        resp.raise_for_status()
        data = resp.json()
        return {"rate": float(data["lastFundingRate"])}
    except Exception as e:
        return {"rate": None, "error": str(e)}


def fetch_funding_okx(symbol: str) -> dict:
    try:
        inst_id = symbol_to_okx(symbol)
        resp = _okx_get(OKX_FUNDING_URLS, {"instId": inst_id})
        data = resp.json()["data"][0]
        return {"rate": float(data["fundingRate"])}
    except Exception as e:
        return {"rate": None, "error": str(e)}


def fetch_funding(exchange: str, symbol: str) -> dict:
    if exchange == "okx":
        return fetch_funding_okx(symbol)
    return fetch_funding_binance_like(exchange, symbol)


def fetch_all_funding(pairs: list) -> dict:
    tasks = []
    for pair in pairs:
        for exchange in pair["exchanges"]:
            tasks.append((pair["symbol"], exchange))

    results = {}
    with ThreadPoolExecutor(max_workers=len(tasks)) as pool:
        futures = {
            pool.submit(fetch_funding, ex, sym): (sym, ex) for sym, ex in tasks
        }
        for future in futures:
            sym, ex = futures[future]
            results.setdefault(sym, {})[ex] = future.result()
    return results


def load_config(path="config.json") -> dict:
    with open(path) as f:
        return json.load(f)


# --- WebSocket price manager ---

class PriceManager:
    """Manages WebSocket connections for real-time price updates."""

    def __init__(self, pairs: list, on_price_update):
        self.pairs = pairs
        self.on_price_update = on_price_update
        # {(symbol, exchange): {"price": float, "price_time": int}}
        self.prices = {}
        self._ws_threads = []

    def start(self):
        for pair in self.pairs:
            symbol = pair["symbol"]
            for exchange in pair["exchanges"]:
                t = threading.Thread(
                    target=self._run_ws,
                    args=(symbol, exchange),
                    daemon=True,
                )
                t.start()
                self._ws_threads.append(t)

    def _run_ws(self, symbol: str, exchange: str):
        while True:
            try:
                if exchange == "okx":
                    self._run_okx_ws(symbol, exchange)
                else:
                    self._run_binance_ws(symbol, exchange)
            except Exception:
                traceback.print_exc()
            # Reconnect after 3s on failure
            import time
            time.sleep(3)
            print(f"[{datetime.now():%H:%M:%S}] Reconnecting WS {exchange}/{symbol}...", flush=True)

    def _run_binance_ws(self, symbol: str, exchange: str):
        url = WS_URLS[exchange].format(symbol_lower=symbol.lower())
        sslopt = {"cert_reqs": ssl.CERT_NONE} if exchange == "aster" else {}
        print(f"[WS {exchange}] Connecting to {url}...", flush=True)

        def on_open(ws):
            print(f"[WS {exchange}] Connected", flush=True)

        def on_message(ws, message):
            try:
                data = json.loads(message)
                bid = float(data["b"])
                ask = float(data["a"])
                mid = (bid + ask) / 2
                price_time = int(data["E"])
                self.prices[(symbol, exchange)] = {
                    "price": mid,
                    "price_time": price_time,
                }
                self.on_price_update()
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS {exchange}] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS {exchange}] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt=sslopt)

    def _run_okx_ws(self, symbol: str, exchange: str):
        url = WS_URLS["okx"]
        inst_id = symbol_to_okx(symbol)
        print(f"[WS okx] Connecting to {url} for {inst_id}...", flush=True)

        def on_open(ws):
            print(f"[WS okx] Connected, subscribing to {inst_id}...", flush=True)
            sub = json.dumps({
                "op": "subscribe",
                "args": [{"channel": "tickers", "instId": inst_id}],
            })
            ws.send(sub)

        def on_message(ws, message):
            try:
                data = json.loads(message)
                if "data" not in data:
                    return
                ticker = data["data"][0]
                price = float(ticker["last"])
                price_time = int(ticker["ts"])
                self.prices[(symbol, exchange)] = {
                    "price": price,
                    "price_time": price_time,
                }
                self.on_price_update()
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS okx] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS okx] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(
            url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close,
        )
        ws.run_forever(sslopt={"cert_reqs": ssl.CERT_NONE})


# --- GUI ---

class FundingMonitor:
    BG = "#1e1e1e"
    FG = "#aaaaaa"
    GREEN = "#4ec9b0"
    RED = "#f44747"
    HEADER_FG = "#dddddd"
    ALERT_BG = "#4a1a1a"
    ALPHA = 0.85

    def __init__(self, config: dict):
        self.config = config
        self.pairs = config["pairs"]
        self.funding_interval = config.get("refresh_interval_seconds", 15) * 1000
        self._flashing = False
        self._flash_count = 0
        # Spread alert counters: {rule_name: consecutive_count}
        self._spread_counts = {
            "bn_close": 0,
            "okx_close": 0,
            "bn_open": 0,
            "okx_open": 0,
        }

        # Latest funding rates: {(symbol, exchange): {"rate": float}}
        self.funding_data = {}

        self.root = tk.Tk()
        self.root.title("Funding Rate")
        self.root.configure(bg=self.BG)
        self.root.attributes("-topmost", True)
        self.root.attributes("-alpha", self.ALPHA)
        self.root.resizable(False, False)
        self.root.overrideredirect(True)

        self.root.bind("<Button-1>", self._start_drag)
        self.root.bind("<B1-Motion>", self._on_drag)

        self._drag_x = 0
        self._drag_y = 0

        self.labels = {}
        self._build_ui()

        # Price manager with WebSocket
        self.price_mgr = PriceManager(self.pairs, self._on_ws_price_update)
        self.price_mgr.start()

        # Funding rate polling
        self.root.after(100, self._refresh_funding)

        # Poll for price updates from WS every 500ms
        self._price_dirty = False
        self.root.after(500, self._poll_prices)

    def _on_ws_price_update(self):
        """Called from WS threads when a new price arrives."""
        self._price_dirty = True

    def _poll_prices(self):
        """Check if WS pushed new prices and update UI."""
        if self._price_dirty:
            self._price_dirty = False
            self._update_prices()
        self.root.after(100, self._poll_prices)

    def _start_drag(self, event):
        self._drag_x = event.x
        self._drag_y = event.y

    def _on_drag(self, event):
        x = self.root.winfo_x() + event.x - self._drag_x
        y = self.root.winfo_y() + event.y - self._drag_y
        self.root.geometry(f"+{x}+{y}")

    def _build_ui(self):
        # Fixed column widths (in characters for Menlo font)
        COL_WIDTHS = {
            "name": 8,
            "price": 10,
            "lag": 8,
            "premium": 16,
            "rate": 10,
        }

        row_idx = 0
        for pair in self.pairs:
            sym = pair["symbol"]

            sym_label = tk.Label(
                self.root, text=sym, font=("Helvetica Neue", 11),
                bg=self.BG, fg=self.HEADER_FG,
            )
            sym_label.grid(row=row_idx, column=0, columnspan=6, sticky="w", padx=8, pady=(6, 2))
            row_idx += 1

            for col, (header, anchor, px, w) in enumerate([
                ("", "w", (14, 4), COL_WIDTHS["name"]),
                ("Price", "e", (4, 4), COL_WIDTHS["price"]),
                ("Lag", "e", (4, 4), COL_WIDTHS["lag"]),
                ("vs Aster", "e", (4, 4), COL_WIDTHS["premium"]),
                ("Rate", "e", (4, 4), COL_WIDTHS["rate"]),
            ]):
                tk.Label(
                    self.root, text=header, font=("Menlo", 9), width=w,
                    bg=self.BG, fg="#666666", anchor=anchor,
                ).grid(row=row_idx, column=col, sticky=anchor, padx=px)
            row_idx += 1

            # Mute buttons dict
            self._mute = {}
            self._mute_btns = {}

            for ex in pair["exchanges"]:
                name_label = tk.Label(
                    self.root, text=f"{ex}", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="w", width=COL_WIDTHS["name"],
                )
                name_label.grid(row=row_idx, column=0, sticky="w", padx=(14, 4))

                price_label = tk.Label(
                    self.root, text="...", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="e", width=COL_WIDTHS["price"],
                )
                price_label.grid(row=row_idx, column=1, sticky="e", padx=(4, 4))

                lag_label = tk.Label(
                    self.root, text="", font=("Menlo", 9),
                    bg=self.BG, fg="#555555", anchor="e", width=COL_WIDTHS["lag"],
                )
                lag_label.grid(row=row_idx, column=2, sticky="e", padx=(4, 4))

                premium_label = tk.Label(
                    self.root, text="", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="e", width=COL_WIDTHS["premium"],
                )
                premium_label.grid(row=row_idx, column=3, sticky="e", padx=(4, 4))

                rate_label = tk.Label(
                    self.root, text="...", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="e", width=COL_WIDTHS["rate"],
                )
                rate_label.grid(row=row_idx, column=4, sticky="e", padx=(4, 4))

                # Mute buttons on the row (only for non-aster exchanges)
                if ex != "aster":
                    ex_key = "bn" if ex == "binance" else "okx"
                    btn_frame = tk.Frame(self.root, bg=self.BG)
                    btn_frame.grid(row=row_idx, column=5, sticky="w", padx=(4, 8))

                    for action in ["平", "开"]:
                        full_action = "平仓" if action == "平" else "开仓"
                        key = (ex_key, full_action)
                        self._mute[key] = False

                        btn = tk.Label(
                            btn_frame, text=f"[{action}]", font=("Menlo", 8),
                            bg=self.BG, fg=self.GREEN, cursor="hand2",
                        )
                        btn.pack(side="left", padx=(0, 2))
                        btn.bind("<Button-1>", lambda e, k=key: self._toggle_mute(k))
                        self._mute_btns[key] = btn

                self.labels[(sym, ex)] = {
                    "name": name_label,
                    "price": price_label,
                    "lag": lag_label,
                    "premium": premium_label,
                    "rate": rate_label,
                    "row": row_idx,
                }
                row_idx += 1

        self.alert_label = tk.Label(
            self.root, text="", font=("Menlo", 9, "bold"),
            bg=self.BG, fg="#ff6666", anchor="w",
        )
        self.alert_label.grid(row=row_idx, column=0, columnspan=6, sticky="w", padx=8, pady=(4, 0))
        row_idx += 1

        self.price_status = tk.Label(
            self.root, text="Price: --", font=("Menlo", 8),
            bg=self.BG, fg="#555555", anchor="w",
        )
        self.price_status.grid(row=row_idx, column=0, columnspan=6, sticky="w", padx=8, pady=(2, 0))
        row_idx += 1

        self.rate_status = tk.Label(
            self.root, text="Rate: --", font=("Menlo", 8),
            bg=self.BG, fg="#555555", anchor="w",
        )
        self.rate_status.grid(row=row_idx, column=0, columnspan=6, sticky="w", padx=8, pady=(0, 6))

    def _toggle_mute(self, key):
        self._mute[key] = not self._mute[key]
        ex_name, action = key
        btn = self._mute_btns[key]
        if self._mute[key]:
            btn.config(text=f"[{action}: OFF]", fg=self.RED)
        else:
            btn.config(text=f"[{action}: ON]", fg=self.GREEN)

    def _refresh_funding(self):
        """Poll funding rates via HTTP every 15s."""
        try:
            print(f"[{datetime.now():%H:%M:%S}] Fetching funding rates...", flush=True)
            results = fetch_all_funding(self.pairs)
            for sym, exchanges in results.items():
                for ex, data in exchanges.items():
                    self.funding_data[(sym, ex)] = data
            self._update_rates()
            self._check_aster_alert()
            now = datetime.now().strftime("%H:%M:%S")
            self.rate_status.config(text=f"Rate: {now}")
            print(f"[{datetime.now():%H:%M:%S}] Funding rates updated", flush=True)
        except Exception:
            traceback.print_exc()
        self.root.after(self.funding_interval, self._refresh_funding)

    def _update_rates(self):
        """Update only the Rate column from funding_data."""
        for pair in self.pairs:
            sym = pair["symbol"]

            # Sort by rate for row ordering
            rows = []
            for ex in pair["exchanges"]:
                rate = self.funding_data.get((sym, ex), {}).get("rate")
                rows.append((ex, rate))
            rows.sort(key=lambda r: (r[1] is None, r[1] if r[1] is not None else 0))

            grid_rows = sorted(
                self.labels[(sym, ex)]["row"] for ex in pair["exchanges"]
            )

            for i, (ex, rate) in enumerate(rows):
                info = self.labels[(sym, ex)]
                target_row = grid_rows[i]
                for key in ("name", "price", "lag", "premium", "rate"):
                    info[key].grid_configure(row=target_row)

                if rate is not None:
                    pct = rate * 100
                    info["rate"].config(
                        text=f"{pct:+.4f}%",
                        fg=self.GREEN if rate >= 0 else self.RED,
                    )
                else:
                    info["rate"].config(text="N/A", fg="#555555")

    def _update_prices(self):
        """Update Price, Lag, and vs Aster columns from WebSocket data."""
        try:
            for pair in self.pairs:
                sym = pair["symbol"]

                # Get aster price
                aster_data = self.price_mgr.prices.get((sym, "aster"))
                aster_price = aster_data["price"] if aster_data else None

                # Find max time for lag calc
                all_times = []
                for ex in pair["exchanges"]:
                    d = self.price_mgr.prices.get((sym, ex))
                    if d and d.get("price_time"):
                        all_times.append(d["price_time"])
                max_time = max(all_times) if all_times else None

                for ex in pair["exchanges"]:
                    info = self.labels[(sym, ex)]
                    ws_data = self.price_mgr.prices.get((sym, ex))

                    if ws_data:
                        price = ws_data["price"]
                        price_time = ws_data["price_time"]

                        info["price"].config(text=f"{price:.4f}", fg=self.FG)

                        # Lag
                        if max_time is not None and price_time is not None:
                            lag_ms = max_time - price_time
                            if lag_ms == 0:
                                info["lag"].config(text="0ms", fg="#555555")
                            else:
                                info["lag"].config(text=f"+{lag_ms}ms", fg="#888888")
                        else:
                            info["lag"].config(text="--", fg="#555555")

                        # Premium vs Aster
                        if ex == "aster":
                            info["premium"].config(text="--", fg="#555555")
                        elif aster_price is not None and aster_price != 0:
                            diff = price - aster_price
                            prem = diff / aster_price * 100
                            info["premium"].config(
                                text=f"{prem:+.2f}% / {diff:+.4f}",
                                fg=self.GREEN if prem >= 0 else self.RED,
                            )
                        else:
                            info["premium"].config(text="N/A", fg="#555555")
                    else:
                        info["price"].config(text="...", fg=self.FG)
                        info["lag"].config(text="--", fg="#555555")
                        info["premium"].config(text="--", fg="#555555")

            # Check BN vs Aster price spread
            self._check_price_spread()

            now = datetime.now().strftime("%H:%M:%S")
            self.price_status.config(text=f"Price: {now}")
        except Exception:
            traceback.print_exc()

    def _check_price_spread(self):
        """Check spread rules and alert accordingly."""
        RULES = [
            # (key, exchange, condition_fn, threshold_desc, color, action)
            ("bn_close",  "binance", lambda d: d > -0.1,  "BN-Aster",  "#ff6666", "平仓"),
            ("okx_close", "okx",     lambda d: d > 0.7,   "OKX-Aster", "#ff6666", "平仓"),
            ("bn_open",   "binance", lambda d: d < -0.3,  "BN-Aster",  self.GREEN, "开仓"),
            ("okx_open",  "okx",     lambda d: d < 0.35,  "OKX-Aster", self.GREEN, "开仓"),
        ]

        alerts = []
        for pair in self.pairs:
            sym = pair["symbol"]
            aster_data = self.price_mgr.prices.get((sym, "aster"))
            if not aster_data:
                continue

            for key, exchange, cond, label, color, action in RULES:
                if exchange not in pair["exchanges"]:
                    continue
                ex_data = self.price_mgr.prices.get((sym, exchange))
                if not ex_data:
                    self._spread_counts[key] = 0
                    continue

                diff = ex_data["price"] - aster_data["price"]
                if cond(diff):
                    self._spread_counts[key] += 1
                else:
                    self._spread_counts[key] = 0

                cnt = self._spread_counts[key]
                if cnt > 0:
                    alerts.append((key, label, diff, cnt, color, action))

        # Filter by mute state
        def is_muted(rule_key):
            # rule_key like "bn_close", "okx_open"
            ex = "bn" if rule_key.startswith("bn") else "okx"
            action = "平仓" if "close" in rule_key else "开仓"
            return self._mute.get((ex, action), False)

        active = [a for a in alerts if not is_muted(a[0])]
        fired = [a for a in active if a[3] >= 5]
        pending = [a for a in active if 0 < a[3] < 5]

        if fired:
            # Show the first fired alert
            key, label, diff, cnt, color, action = fired[0]
            # Extract exchange from key (e.g. "bn_close" -> "binance", "okx_open" -> "okx")
            flash_exchange = "binance" if key.startswith("bn") else "okx"
            self.alert_label.config(
                text=f"!! {action} !! {label}: {diff:+.4f}",
                fg=color,
            )
            if not self._flashing:
                self._flashing = True
                subprocess.Popen(["afplay", "/System/Library/Sounds/Ping.aiff"])
                # Flash the specific exchange row
                for pair in self.pairs:
                    sym = pair["symbol"]
                    if flash_exchange in pair["exchanges"]:
                        flash_color = "#4a1a1a" if "close" in key else "#1a3a1a"
                        self._flash_row(sym, flash_exchange, flash_color)
                print(f"[{datetime.now():%H:%M:%S}] ALERT: {action} {label} {diff:+.4f} (5x)", flush=True)
        elif pending:
            # Show highest count pending
            pending.sort(key=lambda a: -a[3])
            key, label, diff, cnt, color, action = pending[0]
            self.alert_label.config(
                text=f"{label}: {diff:+.4f} ({cnt}/5)",
                fg="#cc8800",
            )
        else:
            self.alert_label.config(text="")

    def _check_aster_alert(self):
        aster_lowest = False
        for pair in self.pairs:
            sym = pair["symbol"]
            if "aster" not in pair["exchanges"]:
                continue
            aster_rate = self.funding_data.get((sym, "aster"), {}).get("rate")
            if aster_rate is None:
                continue
            other_rates = [
                self.funding_data.get((sym, ex), {}).get("rate")
                for ex in pair["exchanges"] if ex != "aster"
            ]
            other_rates = [r for r in other_rates if r is not None]
            if other_rates and aster_rate <= min(other_rates):
                aster_lowest = True
                break

        if aster_lowest and not self._flashing:
            self._flashing = True
            subprocess.Popen(["afplay", "/System/Library/Sounds/Glass.aiff"])
            for pair in self.pairs:
                sym = pair["symbol"]
                if "aster" in pair["exchanges"]:
                    self._flash_row(sym, "aster", "#4a1a1a")
            print(f"[{datetime.now():%H:%M:%S}] ALERT: Aster has lowest rate!", flush=True)

    def _flash_row(self, symbol: str, exchange: str, flash_bg: str):
        """Flash a specific exchange row."""
        key = (symbol, exchange)
        if key not in self.labels:
            return
        info = self.labels[key]
        widgets = [info["name"], info["price"], info["lag"], info["premium"], info["rate"]]
        self._row_flash_count = 0
        self._do_row_flash(widgets, flash_bg, 0)

    def _do_row_flash(self, widgets, flash_bg, count):
        if count >= 6:
            for w in widgets:
                w.configure(bg=self.BG)
            self._flashing = False
            return
        bg = flash_bg if count % 2 == 0 else self.BG
        for w in widgets:
            w.configure(bg=bg)
        self.root.after(300, lambda: self._do_row_flash(widgets, flash_bg, count + 1))

    def run(self):
        self.root.mainloop()


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    cfg = load_config()
    app = FundingMonitor(cfg)
    app.run()
