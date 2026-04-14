import json
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

        self.root.bind("<Button-1>", self._start_drag)
        self.root.bind("<B1-Motion>", self._on_drag)

        self._drag_x = 0
        self._drag_y = 0
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
