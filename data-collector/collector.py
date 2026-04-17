"""
Price Collector - WebSocket → 100ms aggregation → InfluxDB

Collects:
1. Real-time bid/ask prices (bookTicker / tickers) → measurement "price"
2. Index price + mark price (markPrice@1s / index-tickers+mark-price) → measurement "index_price"
3. Orderbook depth (depth20 / books5) → computes Impact Bid/Ask Price → measurement "index_price"

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

import requests
import websocket
from influxdb_client import InfluxDBClient, Point, WritePrecision
from influxdb_client.client.write_api import ASYNCHRONOUS

OKX_FUNDING_URLS = [
    "https://www.okx.com/api/v5/public/funding-rate",
    "https://www.okx.com/priapi/v5/public/funding-rate",
]

# --- WebSocket URLs ---
WS_BOOK_URLS = {
    "aster": "wss://fstream.asterdex.com/ws/{symbol_lower}@bookTicker",
    "binance": "wss://fstream.binance.com/ws/{symbol_lower}@bookTicker",
    "okx": "wss://ws.okx.com:8443/ws/v5/public",
}

WS_MARK_URLS = {
    "aster": "wss://fstream.asterdex.com/ws/{symbol_lower}@markPrice@1s",
    "binance": "wss://fstream.binance.com/ws/{symbol_lower}@markPrice@1s",
    "okx": "wss://ws.okx.com:8443/ws/v5/public",
}

WS_DEPTH_URLS = {
    "aster": "wss://fstream.asterdex.com/ws/{symbol_lower}@depth20@100ms",
    "binance": "wss://fstream.binance.com/ws/{symbol_lower}@depth20@100ms",
    "okx": "wss://ws.okx.com:8443/ws/v5/public",  # subscribe to books5
}

FLUSH_INTERVAL = 0.1  # 100ms


def symbol_to_okx(symbol: str) -> str:
    base = symbol.replace("USDT", "")
    return f"{base}-USDT-SWAP"


def symbol_to_okx_index(symbol: str) -> str:
    base = symbol.replace("USDT", "")
    return f"{base}-USDT"


def load_config(path="config.json") -> dict:
    with open(path) as f:
        return json.load(f)


class TickBuffer:
    """Thread-safe buffer that collects ticks and produces 100ms aggregates."""

    def __init__(self):
        self._lock = threading.Lock()
        self._ticks = defaultdict(list)

    def add(self, symbol: str, exchange: str, bid: float, ask: float, ts_ms: int):
        mid = (bid + ask) / 2
        with self._lock:
            self._ticks[(symbol, exchange)].append({
                "bid": bid, "ask": ask, "mid": mid, "ts": ts_ms,
            })

    def flush(self) -> list:
        with self._lock:
            ticks = dict(self._ticks)
            self._ticks = defaultdict(list)

        points = []
        for (symbol, exchange), tick_list in ticks.items():
            if not tick_list:
                continue
            n = len(tick_list)
            points.append({
                "symbol": symbol,
                "exchange": exchange,
                "bid": sum(t["bid"] for t in tick_list) / n,
                "ask": sum(t["ask"] for t in tick_list) / n,
                "mid": sum(t["mid"] for t in tick_list) / n,
                "ts": max(t["ts"] for t in tick_list),
                "tick_count": n,
            })
        return points


class IndexBuffer:
    """Thread-safe buffer for index price / mark price data (1s granularity)."""

    def __init__(self):
        self._lock = threading.Lock()
        # {(symbol, exchange): {"mark_price": float, "index_price": float, "funding_rate": float, "ts": int}}
        self._latest = {}

    def update(self, symbol: str, exchange: str, **kwargs):
        with self._lock:
            key = (symbol, exchange)
            if key not in self._latest:
                self._latest[key] = {}
            self._latest[key].update(kwargs)

    def flush(self) -> dict:
        with self._lock:
            data = dict(self._latest)
            self._latest = {}
        return data


def calc_impact_price(levels: list, notional: float) -> float | None:
    """
    Calculate impact price by 'eating' through orderbook levels up to notional amount.

    levels: [[price_str, qty_str], ...] — bids or asks from orderbook
    notional: Impact Margin Notional in USDT

    Returns weighted average execution price, or None if insufficient depth.
    """
    if not levels or notional <= 0:
        return None
    cum_notional = 0.0
    cum_qty = 0.0
    for price_str, qty_str, *_ in levels:
        price = float(price_str)
        qty = float(qty_str)
        level_notional = price * qty
        if cum_notional + level_notional >= notional:
            # Partial fill on this level
            remaining = notional - cum_notional
            partial_qty = remaining / price
            cum_qty += partial_qty
            cum_notional = notional
            break
        else:
            cum_notional += level_notional
            cum_qty += qty
    if cum_qty == 0 or cum_notional < notional:
        return None
    return cum_notional / cum_qty


class DepthBuffer:
    """Thread-safe buffer for orderbook depth snapshots and impact price calculation."""

    def __init__(self, symbol_params: dict):
        self._lock = threading.Lock()
        # {(symbol, exchange): {"bids": [...], "asks": [...], "ts": int}}
        self._snapshots = {}
        # {symbol: {exchange: {"impact_notional": float}}}
        self._symbol_params = symbol_params

    def update(self, symbol: str, exchange: str, bids: list, asks: list, ts_ms: int):
        with self._lock:
            self._snapshots[(symbol, exchange)] = {
                "bids": bids, "asks": asks, "ts": ts_ms,
            }

    def flush_impact_prices(self) -> list:
        """Calculate impact prices from latest snapshots and clear them."""
        with self._lock:
            snapshots = dict(self._snapshots)
            self._snapshots = {}

        results = []
        for (symbol, exchange), snap in snapshots.items():
            cfg = self._symbol_params.get(symbol, {}).get(exchange)
            if not cfg:
                continue
            notional = cfg["impact_notional"]
            impact_bid = calc_impact_price(snap["bids"], notional)
            impact_ask = calc_impact_price(snap["asks"], notional)
            if impact_bid is not None or impact_ask is not None:
                results.append({
                    "symbol": symbol,
                    "exchange": exchange,
                    "impact_bid_price": impact_bid,
                    "impact_ask_price": impact_ask,
                    "ts": snap["ts"],
                })
        return results


class PriceCollector:
    """Collects prices via WebSocket and writes to InfluxDB."""

    def __init__(self, config: dict, config_path: str = "config.json"):
        self.config = config
        self.config_path = config_path
        self.pairs = config["pairs"]
        self.buffer = TickBuffer()
        self.index_buffer = IndexBuffer()
        self.depth_buffer = DepthBuffer(config.get("symbol_params", {}))
        self._write_count = 0
        self._last_log = time.time()

        influx_cfg = config["influxdb"]
        self.client = InfluxDBClient(
            url=influx_cfg["url"],
            token=influx_cfg["token"],
            org=influx_cfg["org"],
        )
        self.write_api = self.client.write_api(write_options=ASYNCHRONOUS)
        self.bucket = influx_cfg["bucket"]
        self.org = influx_cfg["org"]

    def _sync_config_loop(self):
        """Sync config from exchange APIs every hour."""
        while True:
            time.sleep(3600)
            try:
                print(f"[{datetime.now():%H:%M:%S}] Hourly config sync...", flush=True)
                from sync_config import sync_all
                updated = sync_all(self.config_path)
                # Reload symbol_params into depth_buffer
                self.depth_buffer._symbol_params = updated.get("symbol_params", {})
                self.config = updated
                print(f"[{datetime.now():%H:%M:%S}] Config sync complete", flush=True)
            except Exception:
                traceback.print_exc()

    def start(self):
        # Sync config from APIs on startup
        try:
            from sync_config import sync_all
            print(f"[{datetime.now():%H:%M:%S}] Syncing config on startup...", flush=True)
            updated = sync_all(self.config_path)
            self.depth_buffer._symbol_params = updated.get("symbol_params", {})
            self.config = updated
        except Exception:
            print(f"[{datetime.now():%H:%M:%S}] Config sync failed, using existing config", flush=True)
            traceback.print_exc()

        # Periodic sync thread
        threading.Thread(target=self._sync_config_loop, daemon=True).start()

        for pair in self.pairs:
            symbol = pair["symbol"]
            for exchange in pair["exchanges"]:
                # bookTicker thread (bid/ask/mid)
                threading.Thread(
                    target=self._run_ws_loop,
                    args=(self._run_book_ws, symbol, exchange),
                    daemon=True,
                ).start()

                # markPrice/index thread
                threading.Thread(
                    target=self._run_ws_loop,
                    args=(self._run_index_ws, symbol, exchange),
                    daemon=True,
                ).start()

                # orderbook depth thread (for impact price)
                if self.config.get("symbol_params", {}).get(symbol, {}).get(exchange):
                    threading.Thread(
                        target=self._run_ws_loop,
                        args=(self._run_depth_ws, symbol, exchange),
                        daemon=True,
                    ).start()

        # OKX funding rate HTTP polling thread
        for pair in self.pairs:
            if "okx" in pair["exchanges"]:
                threading.Thread(
                    target=self._poll_okx_funding_rate,
                    args=(pair["symbol"],),
                    daemon=True,
                ).start()

        self._writer_loop()

    def _poll_okx_funding_rate(self, symbol: str):
        """Poll OKX funding rate via HTTP every 15s."""
        inst_id = symbol_to_okx(symbol)
        print(f"[OKX funding] Starting HTTP polling for {inst_id}...", flush=True)
        while True:
            try:
                for url in OKX_FUNDING_URLS:
                    for verify in (True, False):
                        try:
                            resp = requests.get(url, params={"instId": inst_id}, timeout=10, verify=verify)
                            resp.raise_for_status()
                            data = resp.json()["data"][0]
                            self.index_buffer.update(
                                symbol, "okx",
                                funding_rate=float(data["fundingRate"]),
                            )
                            break
                        except Exception:
                            continue
                    else:
                        continue
                    break
            except Exception:
                traceback.print_exc()
            time.sleep(15)

    def _run_ws_loop(self, ws_func, symbol, exchange):
        """Reconnect loop wrapper."""
        while True:
            try:
                ws_func(symbol, exchange)
            except Exception:
                traceback.print_exc()
            time.sleep(3)
            print(f"[{datetime.now():%H:%M:%S}] Reconnecting {ws_func.__name__} {exchange}/{symbol}...", flush=True)

    def _writer_loop(self):
        print(f"[{datetime.now():%H:%M:%S}] Writer loop started (100ms interval)", flush=True)
        last_index_flush = time.time()

        while True:
            time.sleep(FLUSH_INTERVAL)
            try:
                influx_points = []

                # Flush book ticker data (100ms)
                for d in self.buffer.flush():
                    influx_points.append(
                        Point("price")
                        .tag("exchange", d["exchange"])
                        .tag("symbol", d["symbol"])
                        .field("bid", d["bid"])
                        .field("ask", d["ask"])
                        .field("mid", d["mid"])
                        .field("tick_count", d["tick_count"])
                        .time(d["ts"] * 1_000_000, WritePrecision.NS)
                    )

                # Flush index data every 1s
                now = time.time()
                if now - last_index_flush >= 1.0:
                    last_index_flush = now
                    for (symbol, exchange), d in self.index_buffer.flush().items():
                        point = Point("index_price").tag("exchange", exchange).tag("symbol", symbol)
                        if "mark_price" in d:
                            point = point.field("mark_price", d["mark_price"])
                        if "index_price" in d:
                            point = point.field("index_price", d["index_price"])
                        if "funding_rate" in d:
                            point = point.field("funding_rate", d["funding_rate"])
                        if "ts" in d:
                            point = point.time(d["ts"] * 1_000_000, WritePrecision.NS)
                        else:
                            point = point.time(int(now * 1e9), WritePrecision.NS)
                        influx_points.append(point)

                    # Flush impact prices (calculated from orderbook depth)
                    for d in self.depth_buffer.flush_impact_prices():
                        point = (
                            Point("impact_price")
                            .tag("exchange", d["exchange"])
                            .tag("symbol", d["symbol"])
                        )
                        if d["impact_bid_price"] is not None:
                            point = point.field("impact_bid_price", d["impact_bid_price"])
                        if d["impact_ask_price"] is not None:
                            point = point.field("impact_ask_price", d["impact_ask_price"])
                        if d["ts"]:
                            point = point.time(d["ts"] * 1_000_000, WritePrecision.NS)
                        else:
                            point = point.time(int(now * 1e9), WritePrecision.NS)
                        influx_points.append(point)

                if influx_points:
                    self.write_api.write(bucket=self.bucket, org=self.org, record=influx_points)
                    self._write_count += len(influx_points)

                if now - self._last_log >= 5:
                    ts = datetime.now().strftime("%H:%M:%S")
                    print(f"[{ts}] {self._write_count} points written in last 5s", flush=True)
                    self._write_count = 0
                    self._last_log = now

            except Exception:
                traceback.print_exc()

    # --- Book ticker WebSocket (bid/ask/mid) ---

    def _run_book_ws(self, symbol: str, exchange: str):
        if exchange == "okx":
            self._run_okx_book_ws(symbol, exchange)
        else:
            self._run_binance_book_ws(symbol, exchange)

    def _run_binance_book_ws(self, symbol: str, exchange: str):
        url = WS_BOOK_URLS[exchange].format(symbol_lower=symbol.lower())
        sslopt = {"cert_reqs": ssl.CERT_NONE} if exchange == "aster" else {}
        print(f"[WS {exchange} book] Connecting...", flush=True)

        def on_open(ws):
            print(f"[WS {exchange} book] Connected", flush=True)

        def on_message(ws, message):
            try:
                data = json.loads(message)
                self.buffer.add(symbol, exchange, float(data["b"]), float(data["a"]), int(data["E"]))
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS {exchange} book] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS {exchange} book] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt=sslopt)

    def _run_okx_book_ws(self, symbol: str, exchange: str):
        url = WS_BOOK_URLS["okx"]
        inst_id = symbol_to_okx(symbol)
        print(f"[WS okx book] Connecting for {inst_id}...", flush=True)

        def on_open(ws):
            print(f"[WS okx book] Connected", flush=True)
            ws.send(json.dumps({"op": "subscribe", "args": [{"channel": "tickers", "instId": inst_id}]}))

        def on_message(ws, message):
            try:
                data = json.loads(message)
                if "data" not in data:
                    return
                ticker = data["data"][0]
                self.buffer.add(symbol, exchange, float(ticker["bidPx"]), float(ticker["askPx"]), int(ticker["ts"]))
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS okx book] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS okx book] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt={"cert_reqs": ssl.CERT_NONE})

    # --- Index/Mark price WebSocket ---

    def _run_index_ws(self, symbol: str, exchange: str):
        if exchange == "okx":
            self._run_okx_index_ws(symbol, exchange)
        else:
            self._run_binance_index_ws(symbol, exchange)

    def _run_binance_index_ws(self, symbol: str, exchange: str):
        """Binance/Aster markPrice@1s stream: gives markPrice, indexPrice, fundingRate."""
        url = WS_MARK_URLS[exchange].format(symbol_lower=symbol.lower())
        sslopt = {"cert_reqs": ssl.CERT_NONE} if exchange == "aster" else {}
        print(f"[WS {exchange} index] Connecting...", flush=True)

        def on_open(ws):
            print(f"[WS {exchange} index] Connected", flush=True)

        def on_message(ws, message):
            try:
                data = json.loads(message)
                self.index_buffer.update(
                    symbol, exchange,
                    mark_price=float(data["p"]),
                    index_price=float(data["P"]),
                    funding_rate=float(data["r"]),
                    ts=int(data["E"]),
                )
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS {exchange} index] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS {exchange} index] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt=sslopt)

    def _run_okx_index_ws(self, symbol: str, exchange: str):
        """OKX: subscribe to both index-tickers and mark-price channels."""
        url = WS_MARK_URLS["okx"]
        inst_id_swap = symbol_to_okx(symbol)
        inst_id_index = symbol_to_okx_index(symbol)
        print(f"[WS okx index] Connecting for {inst_id_swap}...", flush=True)

        def on_open(ws):
            print(f"[WS okx index] Connected, subscribing...", flush=True)
            ws.send(json.dumps({
                "op": "subscribe",
                "args": [
                    {"channel": "index-tickers", "instId": inst_id_index},
                    {"channel": "mark-price", "instId": inst_id_swap},
                ],
            }))

        def on_message(ws, message):
            try:
                data = json.loads(message)
                if "data" not in data:
                    return
                channel = data.get("arg", {}).get("channel", "")
                d = data["data"][0]

                if channel == "index-tickers":
                    self.index_buffer.update(
                        symbol, exchange,
                        index_price=float(d["idxPx"]),
                        ts=int(d["ts"]),
                    )
                elif channel == "mark-price":
                    self.index_buffer.update(
                        symbol, exchange,
                        mark_price=float(d["markPx"]),
                        ts=int(d["ts"]),
                    )
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS okx index] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS okx index] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt={"cert_reqs": ssl.CERT_NONE})

    # --- Orderbook depth WebSocket (for impact price) ---

    def _run_depth_ws(self, symbol: str, exchange: str):
        if exchange == "okx":
            self._run_okx_depth_ws(symbol, exchange)
        else:
            self._run_binance_depth_ws(symbol, exchange)

    def _run_binance_depth_ws(self, symbol: str, exchange: str):
        """Binance/Aster depth20@100ms stream."""
        url = WS_DEPTH_URLS[exchange].format(symbol_lower=symbol.lower())
        sslopt = {"cert_reqs": ssl.CERT_NONE} if exchange == "aster" else {}
        print(f"[WS {exchange} depth] Connecting...", flush=True)

        def on_open(ws):
            print(f"[WS {exchange} depth] Connected", flush=True)

        def on_message(ws, message):
            try:
                data = json.loads(message)
                bids = data.get("b", [])
                asks = data.get("a", [])
                ts = int(data.get("E", time.time() * 1000))
                self.depth_buffer.update(symbol, exchange, bids, asks, ts)
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS {exchange} depth] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS {exchange} depth] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt=sslopt)

    def _run_okx_depth_ws(self, symbol: str, exchange: str):
        """OKX books5 channel for top-5 depth."""
        url = WS_DEPTH_URLS["okx"]
        inst_id = symbol_to_okx(symbol)
        print(f"[WS okx depth] Connecting for {inst_id}...", flush=True)

        def on_open(ws):
            print(f"[WS okx depth] Connected, subscribing...", flush=True)
            ws.send(json.dumps({
                "op": "subscribe",
                "args": [{"channel": "books5", "instId": inst_id}],
            }))

        def on_message(ws, message):
            try:
                data = json.loads(message)
                if "data" not in data:
                    return
                d = data["data"][0]
                bids = d.get("bids", [])
                asks = d.get("asks", [])
                ts = int(d.get("ts", time.time() * 1000))
                self.depth_buffer.update(symbol, exchange, bids, asks, ts)
            except Exception:
                traceback.print_exc()

        def on_error(ws, error):
            print(f"[WS okx depth] Error: {error}", flush=True)

        def on_close(ws, code, msg):
            print(f"[WS okx depth] Closed: {code} {msg}", flush=True)

        ws = websocket.WebSocketApp(url, on_open=on_open, on_message=on_message, on_error=on_error, on_close=on_close)
        ws.run_forever(sslopt={"cert_reqs": ssl.CERT_NONE})

    def stop(self):
        self.write_api.close()
        self.client.close()


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    config_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "config.json")
    cfg = load_config()
    collector = PriceCollector(cfg, config_path=config_path)
    try:
        collector.start()
    except KeyboardInterrupt:
        print("\nShutting down...")
        collector.stop()
