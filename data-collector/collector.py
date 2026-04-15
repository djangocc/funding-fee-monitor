"""
Price Collector - WebSocket → 100ms aggregation → InfluxDB

Collects real-time prices from Aster, Binance, OKX via WebSocket.
Aggregates ticks within each 100ms window into a single data point.
Writes to InfluxDB with millisecond precision.
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
from influxdb_client.client.write_api import ASYNCHRONOUS

# --- WebSocket URLs ---
WS_URLS = {
    "aster": "wss://fstream.asterdex.com/ws/{symbol_lower}@bookTicker",
    "binance": "wss://fstream.binance.com/ws/{symbol_lower}@bookTicker",
    "okx": "wss://ws.okx.com:8443/ws/v5/public",
}

FLUSH_INTERVAL = 0.1  # 100ms


def symbol_to_okx(symbol: str) -> str:
    base = symbol.replace("USDT", "")
    return f"{base}-USDT-SWAP"


def load_config(path="config.json") -> dict:
    with open(path) as f:
        return json.load(f)


class TickBuffer:
    """Thread-safe buffer that collects ticks and produces 100ms aggregates."""

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

    def flush(self) -> list:
        """Return list of points and clear buffer.

        Returns: [{"symbol", "exchange", "bid", "ask", "mid", "ts", "tick_count"}, ...]
        """
        with self._lock:
            ticks = dict(self._ticks)
            self._ticks = defaultdict(list)

        points = []
        for (symbol, exchange), tick_list in ticks.items():
            if not tick_list:
                continue
            n = len(tick_list)
            avg_bid = sum(t["bid"] for t in tick_list) / n
            avg_ask = sum(t["ask"] for t in tick_list) / n
            avg_mid = sum(t["mid"] for t in tick_list) / n
            latest_ts = max(t["ts"] for t in tick_list)
            points.append({
                "symbol": symbol,
                "exchange": exchange,
                "bid": avg_bid,
                "ask": avg_ask,
                "mid": avg_mid,
                "ts": latest_ts,
                "tick_count": n,
            })
        return points


class PriceCollector:
    """Collects prices via WebSocket and writes 100ms aggregates to InfluxDB."""

    def __init__(self, config: dict):
        self.config = config
        self.pairs = config["pairs"]
        self.buffer = TickBuffer()
        self._write_count = 0
        self._last_log = time.time()

        # InfluxDB client
        influx_cfg = config["influxdb"]
        self.client = InfluxDBClient(
            url=influx_cfg["url"],
            token=influx_cfg["token"],
            org=influx_cfg["org"],
        )
        self.write_api = self.client.write_api(write_options=ASYNCHRONOUS)
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
        """Flush buffer every 100ms and write to InfluxDB."""
        print(f"[{datetime.now():%H:%M:%S}] Writer loop started (100ms interval)", flush=True)
        while True:
            time.sleep(FLUSH_INTERVAL)
            try:
                data_points = self.buffer.flush()
                if not data_points:
                    continue

                influx_points = []
                for d in data_points:
                    point = (
                        Point("price")
                        .tag("exchange", d["exchange"])
                        .tag("symbol", d["symbol"])
                        .field("bid", d["bid"])
                        .field("ask", d["ask"])
                        .field("mid", d["mid"])
                        .field("tick_count", d["tick_count"])
                        .time(d["ts"] * 1_000_000, WritePrecision.NS)  # ms → ns
                    )
                    influx_points.append(point)

                self.write_api.write(bucket=self.bucket, org=self.org, record=influx_points)
                self._write_count += len(influx_points)

                # Log summary every 5 seconds
                now = time.time()
                if now - self._last_log >= 5:
                    ts = datetime.now().strftime("%H:%M:%S")
                    print(f"[{ts}] {self._write_count} points written in last 5s", flush=True)
                    self._write_count = 0
                    self._last_log = now

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
        self.write_api.close()
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
