"""
FastAPI backend for KLineChart.

Serves OHLCV data from InfluxDB and provides WebSocket for real-time updates.
"""

import asyncio
import json
import os
import time
from datetime import datetime, timezone
from typing import Optional

from fastapi import FastAPI, Query, WebSocket, WebSocketDisconnect
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from influxdb_client import InfluxDBClient

# Config
INFLUX_URL = os.getenv("INFLUX_URL", "http://localhost:8101")
INFLUX_TOKEN = os.getenv("INFLUX_TOKEN", "funding-fee-monitor-token")
INFLUX_ORG = os.getenv("INFLUX_ORG", "funding")
INFLUX_BUCKET = os.getenv("INFLUX_BUCKET", "prices")

app = FastAPI(title="Funding Fee Chart API")
app.mount("/static", StaticFiles(directory="static"), name="static")

# InfluxDB client
client = InfluxDBClient(url=INFLUX_URL, token=INFLUX_TOKEN, org=INFLUX_ORG)
query_api = client.query_api()

# Period to Flux window mapping
PERIOD_MAP = {
    "1s": "1s",
    "5s": "5s",
    "15s": "15s",
    "30s": "30s",
    "1m": "1m",
    "5m": "5m",
    "15m": "15m",
    "1h": "1h",
    "4h": "4h",
    "1d": "1d",
}

# Default range for each period
DEFAULT_RANGE = {
    "1s": "-10m",
    "5s": "-30m",
    "15s": "-1h",
    "30s": "-2h",
    "1m": "-6h",
    "5m": "-1d",
    "15m": "-3d",
    "1h": "-7d",
    "4h": "-30d",
    "1d": "-90d",
}


def query_ohlcv(exchange: str, symbol: str, period: str,
                start: Optional[str] = None, end: Optional[str] = None) -> list:
    """Query InfluxDB and aggregate mid price into OHLCV."""
    window = PERIOD_MAP.get(period, "1m")
    range_start = start or DEFAULT_RANGE.get(period, "-6h")
    range_end = end or "now()"

    flux = f'''
from(bucket: "{INFLUX_BUCKET}")
  |> range(start: {range_start}, stop: {range_end})
  |> filter(fn: (r) => r._measurement == "price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "mid")
  |> aggregateWindow(every: {window}, fn: first, createEmpty: false)
  |> yield(name: "open")
'''
    flux_high = f'''
from(bucket: "{INFLUX_BUCKET}")
  |> range(start: {range_start}, stop: {range_end})
  |> filter(fn: (r) => r._measurement == "price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "mid")
  |> aggregateWindow(every: {window}, fn: max, createEmpty: false)
  |> yield(name: "high")
'''
    flux_low = f'''
from(bucket: "{INFLUX_BUCKET}")
  |> range(start: {range_start}, stop: {range_end})
  |> filter(fn: (r) => r._measurement == "price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "mid")
  |> aggregateWindow(every: {window}, fn: min, createEmpty: false)
  |> yield(name: "low")
'''
    flux_close = f'''
from(bucket: "{INFLUX_BUCKET}")
  |> range(start: {range_start}, stop: {range_end})
  |> filter(fn: (r) => r._measurement == "price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "mid")
  |> aggregateWindow(every: {window}, fn: last, createEmpty: false)
  |> yield(name: "close")
'''

    full_query = flux + flux_high + flux_low + flux_close
    tables = query_api.query(full_query)

    # Organize by yield name
    data = {}  # {yield_name: {timestamp: value}}
    for table in tables:
        for record in table.records:
            name = record.values.get("result", "")
            ts = int(record.get_time().timestamp() * 1000)
            val = record.get_value()
            if name not in data:
                data[name] = {}
            data[name][ts] = val

    # Merge into OHLCV
    opens = data.get("open", {})
    highs = data.get("high", {})
    lows = data.get("low", {})
    closes = data.get("close", {})

    all_ts = sorted(set(opens.keys()) & set(highs.keys()) & set(lows.keys()) & set(closes.keys()))

    result = []
    for ts in all_ts:
        result.append({
            "timestamp": ts,
            "open": opens[ts],
            "high": highs[ts],
            "low": lows[ts],
            "close": closes[ts],
        })
    return result


def query_spread_ohlcv(base: str, target: str, symbol: str, period: str,
                       start: Optional[str] = None, end: Optional[str] = None) -> list:
    """Query spread (target - base) as OHLCV by querying both exchanges and computing in Python."""
    base_ohlcv = query_ohlcv(base, symbol, period, start, end)
    target_ohlcv = query_ohlcv(target, symbol, period, start, end)

    # Index by timestamp
    base_by_ts = {bar["timestamp"]: bar for bar in base_ohlcv}
    target_by_ts = {bar["timestamp"]: bar for bar in target_ohlcv}

    common_ts = sorted(set(base_by_ts.keys()) & set(target_by_ts.keys()))

    result = []
    for ts in common_ts:
        b = base_by_ts[ts]
        t = target_by_ts[ts]
        # Use same-type price diff to avoid cross-extreme artifacts
        spread_open = t["open"] - b["open"]
        spread_high = t["high"] - b["high"]
        spread_low = t["low"] - b["low"]
        spread_close = t["close"] - b["close"]
        # Ensure high >= low
        actual_high = max(spread_open, spread_high, spread_low, spread_close)
        actual_low = min(spread_open, spread_high, spread_low, spread_close)
        result.append({
            "timestamp": ts,
            "open": spread_open,
            "high": actual_high,
            "low": actual_low,
            "close": spread_close,
        })
    return result


# --- API Endpoints ---

@app.get("/")
async def index():
    return FileResponse("static/index.html")


@app.get("/api/kline")
async def get_kline(
    exchange: str = Query(default="aster"),
    symbol: str = Query(default="RAVEUSDT"),
    period: str = Query(default="1m"),
    start: Optional[str] = Query(default=None),
    end: Optional[str] = Query(default=None),
):
    """Get OHLCV data for a single exchange."""
    data = query_ohlcv(exchange, symbol, period, start, end)
    return {"data": data, "count": len(data)}


@app.get("/api/spread")
async def get_spread(
    base: str = Query(default="aster"),
    target: str = Query(default="binance"),
    symbol: str = Query(default="RAVEUSDT"),
    period: str = Query(default="1m"),
    start: Optional[str] = Query(default=None),
    end: Optional[str] = Query(default=None),
):
    """Get spread (target - base) as OHLCV data."""
    data = query_spread_ohlcv(base, target, symbol, period, start, end)
    return {"data": data, "count": len(data)}


@app.get("/api/exchanges")
async def get_exchanges():
    """List available exchanges."""
    return ["aster", "binance", "okx"]


@app.get("/api/periods")
async def get_periods():
    """List available periods."""
    return list(PERIOD_MAP.keys())


# --- WebSocket for real-time updates ---

class ConnectionManager:
    def __init__(self):
        self.connections: list[WebSocket] = []

    async def connect(self, ws: WebSocket):
        await ws.accept()
        self.connections.append(ws)

    def disconnect(self, ws: WebSocket):
        self.connections.remove(ws)

    async def broadcast(self, message: dict):
        for ws in self.connections[:]:
            try:
                await ws.send_json(message)
            except Exception:
                self.connections.remove(ws)


manager = ConnectionManager()


@app.websocket("/ws/kline")
async def ws_kline(ws: WebSocket):
    """WebSocket endpoint for real-time K-line updates.

    Client sends: {"exchange": "aster", "symbol": "RAVEUSDT", "period": "1m"}
    Server pushes: latest OHLCV bar every second.
    """
    await manager.connect(ws)
    try:
        # Wait for subscription message
        sub = await ws.receive_json()
        exchange = sub.get("exchange", "aster")
        symbol = sub.get("symbol", "RAVEUSDT")
        period = sub.get("period", "1m")
        window = PERIOD_MAP.get(period, "1m")

        while True:
            await asyncio.sleep(1)
            try:
                # Get the latest bar
                flux = f'''
from(bucket: "{INFLUX_BUCKET}")
  |> range(start: -{window})
  |> filter(fn: (r) => r._measurement == "price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "mid")
'''
                tables = query_api.query(flux)
                values = []
                for table in tables:
                    for record in table.records:
                        values.append(record.get_value())

                if values:
                    now_ts = int(time.time() * 1000)
                    # Align to period
                    period_ms = _period_to_ms(period)
                    aligned_ts = (now_ts // period_ms) * period_ms

                    bar = {
                        "timestamp": aligned_ts,
                        "open": values[0],
                        "high": max(values),
                        "low": min(values),
                        "close": values[-1],
                    }
                    await ws.send_json(bar)
            except Exception:
                pass

    except WebSocketDisconnect:
        manager.disconnect(ws)


def _period_to_ms(period: str) -> int:
    units = {"s": 1000, "m": 60000, "h": 3600000, "d": 86400000}
    num = int(period[:-1])
    unit = period[-1]
    return num * units.get(unit, 60000)


if __name__ == "__main__":
    import uvicorn
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    uvicorn.run(app, host="0.0.0.0", port=8102)
