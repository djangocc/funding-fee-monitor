"""
Price Collector - WebSocket → 1s aggregation → InfluxDB

Collects real-time prices from Aster, Binance, OKX via WebSocket.
Aggregates multiple ticks within each second into a single data point (average).
Writes to InfluxDB every second.
"""

import json
import os
import ssl
import threading
import time
import traceback
from collections import defaultdict
from datetime import datetime, timezone

import websocket
from influxdb_client import InfluxDBClient, Point, WritePrecision
from influxdb_client.client.write_api import SYNCHRONOUS

# --- WebSocket URLs ---
WS_URLS = {
    "aster": "wss://fstream.asterdex.com/ws/{symbol_lower}@bookTicker",
    "binance": "wss://fstream.binance.com/ws/{symbol_lower}@bookTicker",
    "okx": "wss://ws.okx.com:8443/ws/v5/public",
}


def symbol_to_okx(symbol: str) -> str:
    base = symbol.replace("USDT", "")
    return f"{base}-USDT-SWAP"


def load_config(path="config.json") -> dict:
    with open(path) as f:
        return json.load(f)


class TickBuffer:
    """Thread-safe buffer that collects ticks and produces 1s aggregates."""

    def __init__(self):
        self._lock = threading.Lock()
        # {(symbol, exchange): [{"bid": float, "ask": float, "mid": float, "ts": int}, ...]}
        self._ticks = defaultdict(list)

    def add(self, symbol: str, exchange: str, bid: float, ask: float, ts_ms: int):
        mid = (bid + ask) / 2
        with self._lock:
            self._ticks[(symbol, exchange)].append({
                "bid": bid,
                "ask": ask,
                "mid": mid,
                "ts": ts_ms,
            })

    def flush(self) -> dict:
        """Return 1s aggregates and clear buffer.

        Returns: {(symbol, exchange): {"bid": avg, "ask": avg, "mid": avg, "ts": latest}}
        """
        with self._lock:
            ticks = dict(self._ticks)
            self._ticks = defaultdict(list)

        result = {}
        for key, tick_list in ticks.items():
            if not tick_list:
                continue
            n = len(tick_list)
            avg_bid = sum(t["bid"] for t in tick_list) / n
            avg_ask = sum(t["ask"] for t in tick_list) / n
            avg_mid = sum(t["mid"] for t in tick_list) / n
            latest_ts = max(t["ts"] for t in tick_list)
            result[key] = {
                "bid": avg_bid,
                "ask": avg_ask,
                "mid": avg_mid,
                "ts": latest_ts,
                "tick_count": n,
            }
        return result


class PriceCollector:
    """Collects prices via WebSocket and writes 1s aggregates to InfluxDB."""

    def __init__(self, config: dict):
        self.config = config
        self.pairs = config["pairs"]
        self.buffer = TickBuffer()

        # InfluxDB client
        influx_cfg = config["influxdb"]
        self.client = InfluxDBClient(
            url=influx_cfg["url"],
            token=influx_cfg["token"],
            org=influx_cfg["org"],
        )
        self.write_api = self.client.write_api(write_options=SYNCHRONOUS)
        self.bucket = influx_cfg["bucket"]
        self.org = influx_cfg["org"]

    def start(self):
        # Start WS threads
        for pair in self.pairs:
            symbol = pair["symbol"]
            for exchange in pair["exchanges"]:
                t = threading.Thread(
                    target=self._run_ws,
                    args=(symbol, exchange),
                    daemon=True,
                )
                t.start()

        # Start writer loop
        self._writer_loop()

    def _writer_loop(self):
        """Flush buffer every 1s and write to InfluxDB."""
        print(f"[{datetime.now():%H:%M:%S}] Writer loop started", flush=True)
        while True:
            time.sleep(1)
            try:
                aggregates = self.buffer.flush()
                if not aggregates:
                    continue

                points = []
                for (symbol, exchange), data in aggregates.items():
                    point = (
                        Point("price")
                        .tag("exchange", exchange)
                        .tag("symbol", symbol)
                        .field("bid", data["bid"])
                        .field("ask", data["ask"])
                        .field("mid", data["mid"])
                        .field("tick_count", data["tick_count"])
                        .time(datetime.now(timezone.utc), WritePrecision.S)
                    )
                    points.append(point)

                self.write_api.write(bucket=self.bucket, org=self.org, record=points)

                ts = datetime.now().strftime("%H:%M:%S")
                summary = ", ".join(
                    f"{ex}={d['mid']:.4f}({d['tick_count']}t)"
                    for (sym, ex), d in sorted(aggregates.items())
                )
                print(f"[{ts}] Wrote {len(points)} points: {summary}", flush=True)

            except Exception:
                traceback.print_exc()

    # --- WebSocket handlers ---

    def _run_ws(self, symbol: str, exchange: str):
        while True:
            try:
                if exchange == "okx":
                    self._run_okx_ws(symbol, exchange)
                else:
                    self._run_binance_ws(symbol, exchange)
            except Exception:
                traceback.print_exc()
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
                ts = int(data["E"])
                self.buffer.add(symbol, exchange, bid, ask, ts)
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS {exchange}] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS {exchange}] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(
            url, on_open=on_open, on_message=on_message,
            on_error=on_error, on_close=on_close,
        )
        ws.run_forever(sslopt=sslopt)

    def _run_okx_ws(self, symbol: str, exchange: str):
        url = WS_URLS["okx"]
        inst_id = symbol_to_okx(symbol)
        print(f"[WS okx] Connecting for {inst_id}...", flush=True)

        def on_open(ws):
            print(f"[WS okx] Connected, subscribing to {inst_id}...", flush=True)
            ws.send(json.dumps({
                "op": "subscribe",
                "args": [{"channel": "tickers", "instId": inst_id}],
            }))

        def on_message(ws, message):
            try:
                data = json.loads(message)
                if "data" not in data:
                    return
                ticker = data["data"][0]
                bid = float(ticker["bidPx"])
                ask = float(ticker["askPx"])
                ts = int(ticker["ts"])
                self.buffer.add(symbol, exchange, bid, ask, ts)
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS okx] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS okx] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(
            url, on_open=on_open, on_message=on_message,
            on_error=on_error, on_close=on_close,
        )
        ws.run_forever(sslopt={"cert_reqs": ssl.CERT_NONE})

    def stop(self):
        self.client.close()


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    cfg = load_config()
    collector = PriceCollector(cfg)
    try:
        collector.start()
    except KeyboardInterrupt:
        print("\nShutting down...")
        collector.stop()
