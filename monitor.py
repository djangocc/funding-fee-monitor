import json
import subprocess
import threading
import tkinter as tk
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime

import requests

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
    if exchange == "okx":
        return fetch_okx(symbol)
    return fetch_binance_like(exchange, symbol)


def fetch_all_rates(pairs: list) -> dict:
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


def load_config(path="config.json") -> dict:
    with open(path) as f:
        return json.load(f)


class FundingMonitor:
    BG = "#1e1e1e"
    FG = "#aaaaaa"
    GREEN = "#4ec9b0"
    RED = "#f44747"
    HEADER_FG = "#dddddd"
    ALPHA = 0.85

    def __init__(self, config: dict):
        self.config = config
        self.pairs = config["pairs"]
        self.interval = config.get("refresh_interval_seconds", 60) * 1000

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

        # Container for dynamic rate rows
        self.row_frames = {}
        self._build_ui()

        # Schedule first fetch after mainloop starts
        self.root.after(100, self._refresh)

    def _start_drag(self, event):
        self._drag_x = event.x
        self._drag_y = event.y

    def _on_drag(self, event):
        x = self.root.winfo_x() + event.x - self._drag_x
        y = self.root.winfo_y() + event.y - self._drag_y
        self.root.geometry(f"+{x}+{y}")

    def _build_ui(self):
        title = tk.Label(
            self.root, text="Funding Rates", font=("Helvetica Neue", 12),
            bg=self.BG, fg=self.HEADER_FG,
        )
        title.pack(pady=(6, 2))

        for pair in self.pairs:
            sym = pair["symbol"]

            sym_label = tk.Label(
                self.root, text=sym, font=("Helvetica Neue", 10),
                bg=self.BG, fg=self.HEADER_FG,
            )
            sym_label.pack(anchor="w", padx=8, pady=(4, 0))

            container = tk.Frame(self.root, bg=self.BG)
            container.pack(anchor="w", fill="x")
            self.row_frames[sym] = {
                "container": container,
                "exchanges": pair["exchanges"],
            }

            # Initial "loading..." rows
            for ex in pair["exchanges"]:
                self._make_row(container, ex, "loading...", self.FG)

        self.status_label = tk.Label(
            self.root, text="", font=("Helvetica Neue", 8),
            bg=self.BG, fg="#555555",
        )
        self.status_label.pack(pady=(2, 6))

    def _make_row(self, container, exchange, text, fg):
        frame = tk.Frame(container, bg=self.BG)
        frame.pack(anchor="w", padx=14, pady=0)

        tk.Label(
            frame, text=f"{exchange}:", font=("Menlo", 10),
            bg=self.BG, fg=self.FG, width=10, anchor="w",
        ).pack(side="left")

        tk.Label(
            frame, text=text, font=("Menlo", 10),
            bg=self.BG, fg=fg, anchor="e", width=12,
        ).pack(side="left")

    def _refresh(self):
        def do_fetch():
            results = fetch_all_rates(self.pairs)
            self.root.after(0, lambda: self._update_ui(results))

        threading.Thread(target=do_fetch, daemon=True).start()
        self.root.after(self.interval, self._refresh)

    def _update_ui(self, results: dict):
        for sym, info in self.row_frames.items():
            container = info["container"]

            # Clear old rows
            for w in container.winfo_children():
                w.destroy()

            # Build sorted rows
            exchange_data = results.get(sym, {})
            rows = []
            for ex in info["exchanges"]:
                data = exchange_data.get(ex, {"rate": None})
                rate = data.get("rate")
                rows.append((ex, rate))

            # Sort by rate ascending (most negative first), None at end
            rows.sort(key=lambda r: (r[1] is None, r[1] if r[1] is not None else 0))

            for ex, rate in rows:
                if rate is not None:
                    pct = rate * 100
                    text = f"{pct:+.4f}%"
                    fg = self.GREEN if rate >= 0 else self.RED
                else:
                    text = "N/A"
                    fg = "#555555"
                self._make_row(container, ex, text, fg)

        now = datetime.now().strftime("%H:%M:%S")
        self.status_label.config(text=f"Updated: {now}")

    def run(self):
        self.root.mainloop()


if __name__ == "__main__":
    cfg = load_config()
    app = FundingMonitor(cfg)
    app.run()
