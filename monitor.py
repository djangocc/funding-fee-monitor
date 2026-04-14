import json
import os
import subprocess
import tkinter as tk
import traceback
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime

import requests

EXCHANGE_URLS = {
    "aster": "https://fapi.asterdex.com/fapi/v1/premiumIndex",
    "binance": "https://fapi.binance.com/fapi/v1/premiumIndex",
}
OKX_FUNDING_URL = "https://www.okx.com/api/v5/public/funding-rate"
OKX_TICKER_URL = "https://www.okx.com/api/v5/market/ticker"
REQUEST_TIMEOUT = 10


def symbol_to_okx(symbol: str) -> str:
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
            "price": float(data["markPrice"]),
        }
    except Exception as e:
        return {"rate": None, "price": None, "error": str(e)}


def fetch_okx(symbol: str) -> dict:
    try:
        inst_id = symbol_to_okx(symbol)
        resp = requests.get(OKX_FUNDING_URL, params={"instId": inst_id}, timeout=REQUEST_TIMEOUT)
        resp.raise_for_status()
        funding = resp.json()["data"][0]
        rate = float(funding["fundingRate"])

        resp2 = requests.get(OKX_TICKER_URL, params={"instId": inst_id}, timeout=REQUEST_TIMEOUT)
        resp2.raise_for_status()
        ticker = resp2.json()["data"][0]
        price = float(ticker["last"])

        return {"rate": rate, "price": price}
    except Exception as e:
        return {"rate": None, "price": None, "error": str(e)}


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
    ALERT_BG = "#4a1a1a"
    ALPHA = 0.85

    def __init__(self, config: dict):
        self.config = config
        self.pairs = config["pairs"]
        self.interval = config.get("refresh_interval_seconds", 60) * 1000
        self._flashing = False
        self._flash_count = 0

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
        self.root.after(100, self._refresh)

    def _start_drag(self, event):
        self._drag_x = event.x
        self._drag_y = event.y

    def _on_drag(self, event):
        x = self.root.winfo_x() + event.x - self._drag_x
        y = self.root.winfo_y() + event.y - self._drag_y
        self.root.geometry(f"+{x}+{y}")

    def _build_ui(self):
        row_idx = 0
        for pair in self.pairs:
            sym = pair["symbol"]

            sym_label = tk.Label(
                self.root, text=sym, font=("Helvetica Neue", 11),
                bg=self.BG, fg=self.HEADER_FG,
            )
            sym_label.grid(row=row_idx, column=0, columnspan=3, sticky="w", padx=8, pady=(6, 2))
            row_idx += 1

            # Column headers
            for col, header in enumerate(["", "Price", "Rate"]):
                tk.Label(
                    self.root, text=header, font=("Menlo", 9),
                    bg=self.BG, fg="#666666", anchor="e" if col > 0 else "w",
                ).grid(row=row_idx, column=col, sticky="e" if col > 0 else "w",
                       padx=(14, 4) if col == 0 else (4, 8))
            row_idx += 1

            for ex in pair["exchanges"]:
                name_label = tk.Label(
                    self.root, text=f"{ex}", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="w",
                )
                name_label.grid(row=row_idx, column=0, sticky="w", padx=(14, 4))

                price_label = tk.Label(
                    self.root, text="...", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="e",
                )
                price_label.grid(row=row_idx, column=1, sticky="e", padx=(4, 4))

                rate_label = tk.Label(
                    self.root, text="...", font=("Menlo", 10),
                    bg=self.BG, fg=self.FG, anchor="e",
                )
                rate_label.grid(row=row_idx, column=2, sticky="e", padx=(4, 8))

                self.labels[(sym, ex)] = {
                    "name": name_label,
                    "price": price_label,
                    "rate": rate_label,
                    "row": row_idx,
                }
                row_idx += 1

        self.status_label = tk.Label(
            self.root, text="", font=("Helvetica Neue", 8),
            bg=self.BG, fg="#555555",
        )
        self.status_label.grid(row=row_idx, column=0, columnspan=3, pady=(2, 6))

    def _refresh(self):
        try:
            print(f"[{datetime.now():%H:%M:%S}] Fetching...", flush=True)
            results = fetch_all_rates(self.pairs)
            print(f"[{datetime.now():%H:%M:%S}] Got: {results}", flush=True)
            self._update_ui(results)
        except Exception:
            traceback.print_exc()
        self.root.after(self.interval, self._refresh)

    def _update_ui(self, results: dict):
        try:
            for pair in self.pairs:
                sym = pair["symbol"]
                exchange_data = results.get(sym, {})

                rows = []
                for ex in pair["exchanges"]:
                    data = exchange_data.get(ex, {})
                    rate = data.get("rate")
                    price = data.get("price")
                    rows.append((ex, rate, price))

                rows.sort(key=lambda r: (r[1] is None, r[1] if r[1] is not None else 0))

                grid_rows = sorted(
                    self.labels[(sym, ex)]["row"] for ex in pair["exchanges"]
                )

                for i, (ex, rate, price) in enumerate(rows):
                    info = self.labels[(sym, ex)]
                    target_row = grid_rows[i]

                    info["name"].grid_configure(row=target_row)
                    info["price"].grid_configure(row=target_row)
                    info["rate"].grid_configure(row=target_row)

                    if price is not None:
                        info["price"].config(text=f"{price:.4f}", fg=self.FG)
                    else:
                        info["price"].config(text="N/A", fg="#555555")

                    if rate is not None:
                        pct = rate * 100
                        info["rate"].config(
                            text=f"{pct:+.4f}%",
                            fg=self.GREEN if rate >= 0 else self.RED,
                        )
                    else:
                        info["rate"].config(text="N/A", fg="#555555")

            # Check if aster has the lowest rate for any pair
            aster_lowest = False
            for pair in self.pairs:
                sym = pair["symbol"]
                if "aster" not in pair["exchanges"]:
                    continue
                exchange_data = results.get(sym, {})
                aster_rate = exchange_data.get("aster", {}).get("rate")
                if aster_rate is None:
                    continue
                other_rates = [
                    exchange_data.get(ex, {}).get("rate")
                    for ex in pair["exchanges"] if ex != "aster"
                ]
                other_rates = [r for r in other_rates if r is not None]
                if other_rates and aster_rate <= min(other_rates):
                    aster_lowest = True
                    break

            if aster_lowest and not self._flashing:
                self._flashing = True
                self._alert()
                print(f"[{datetime.now():%H:%M:%S}] ALERT: Aster has lowest rate!", flush=True)

            now = datetime.now().strftime("%H:%M:%S")
            self.status_label.config(text=f"Updated: {now}")
            print(f"[{datetime.now():%H:%M:%S}] UI updated", flush=True)
        except Exception:
            traceback.print_exc()

    def _alert(self):
        subprocess.Popen(["afplay", "/System/Library/Sounds/Glass.aiff"])
        self._flash_count = 0
        self._flash()

    def _flash(self):
        if self._flash_count >= 6:
            self.root.configure(bg=self.BG)
            self._set_all_bg(self.BG)
            self._flashing = False
            return
        bg = self.ALERT_BG if self._flash_count % 2 == 0 else self.BG
        self.root.configure(bg=bg)
        self._set_all_bg(bg)
        self._flash_count += 1
        self.root.after(300, self._flash)

    def _set_all_bg(self, bg):
        for widget in self.root.winfo_children():
            try:
                widget.configure(bg=bg)
            except tk.TclError:
                pass

    def run(self):
        self.root.mainloop()


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    cfg = load_config()
    app = FundingMonitor(cfg)
    app.run()
