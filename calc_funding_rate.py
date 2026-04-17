"""
Calculate estimated funding rate per exchange using platform-specific rules.

Reads config from data-collector/config.json:
  - platform_profiles: sampling frequency, averaging method, formula type
  - symbol_params: dynamic params (interest_rate, cap/floor, interval, etc.)

Uses impact_price measurement (impact_bid_price / impact_ask_price) from InfluxDB
instead of best bid/ask, for more accurate Premium Index calculation.

Formulas:
  Premium Index: P = [Max(0, ImpactBid - Index) - Max(0, Index - ImpactAsk)] / Index

  Binance/Aster:
    FR = [AvgPremium + clamp(IR - AvgPremium, -0.05%, 0.05%)] / (8 / N)
    then clamp to [floor, cap]
    1h: equal-weight avg; >1h: time-weighted avg

  OKX:
    FR = clamp(AvgPremium + clamp(IR - AvgPremium, -0.05%, 0.05%), floor, cap)
    always time-weighted avg

Writes to InfluxDB measurement "calculated_funding_rate".
"""

import json
import os
from datetime import datetime, timezone

from influxdb_client import InfluxDBClient, Point, WritePrecision
from influxdb_client.client.write_api import SYNCHRONOUS

CONFIG_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                           "data-collector", "config.json")

INNER_CLAMP = 0.0005  # ±0.05%, constant across all platforms


def load_config():
    with open(CONFIG_PATH) as f:
        return json.load(f)


# ============================================================
# Averaging methods
# ============================================================

def equal_avg(premiums: list) -> float:
    return sum(premiums) / len(premiums)


def time_weighted_avg(premiums: list) -> float:
    n = len(premiums)
    weighted = sum((i + 1) * p for i, p in enumerate(premiums))
    denom = n * (n + 1) / 2
    return weighted / denom


# ============================================================
# Funding Rate formulas
# ============================================================

def fr_binance(avg_premium: float, interest_rate: float, interval_hours: int,
               cap: float | None, floor: float | None) -> float:
    """Binance formula: [AvgP + clamp(IR-AvgP)] / (8/N), then clamp to cap/floor."""
    clamp_val = max(-INNER_CLAMP, min(INNER_CLAMP, interest_rate - avg_premium))
    raw = (avg_premium + clamp_val) / (8 / interval_hours)
    if cap is not None and floor is not None:
        raw = max(floor, min(cap, raw))
    return raw


def fr_okx(avg_premium: float, interest_rate: float, interval_hours: int,
           cap: float | None, floor: float | None) -> float:
    """OKX formula: clamp(AvgP + clamp(IR-AvgP), floor, cap). No (8/N) divisor."""
    clamp_val = max(-INNER_CLAMP, min(INNER_CLAMP, interest_rate - avg_premium))
    raw = avg_premium + clamp_val
    if cap is not None and floor is not None:
        raw = max(floor, min(cap, raw))
    return raw


FR_FUNCS = {
    "binance": fr_binance,
    "okx": fr_okx,
}


# ============================================================
# Data querying
# ============================================================

def query_premium_samples(query_api, exchange: str, symbol: str,
                          bucket: str, sample_window: str, lookback: str) -> list:
    """
    Query impact_bid/ask and index_price, compute Premium Index at each sample.
    Returns list of premium values in chronological order.
    """
    # Impact prices
    flux_impact = f'''
from(bucket: "{bucket}")
  |> range(start: {lookback})
  |> filter(fn: (r) => r._measurement == "impact_price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> aggregateWindow(every: {sample_window}, fn: last, createEmpty: false)
'''
    # Index price
    flux_index = f'''
from(bucket: "{bucket}")
  |> range(start: {lookback})
  |> filter(fn: (r) => r._measurement == "index_price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "index_price")
  |> aggregateWindow(every: {sample_window}, fn: last, createEmpty: false)
'''

    # Parse impact data
    impact_data = {}
    for table in query_api.query(flux_impact):
        for r in table.records:
            ts = int(r.get_time().timestamp())
            impact_data.setdefault(ts, {})[r.get_field()] = r.get_value()

    # Parse index data
    index_data = {}
    for table in query_api.query(flux_index):
        for r in table.records:
            ts = int(r.get_time().timestamp())
            index_data[ts] = r.get_value()

    # Compute premium at each overlapping timestamp
    common_ts = sorted(set(impact_data.keys()) & set(index_data.keys()))
    premiums = []
    for ts in common_ts:
        imp = impact_data[ts]
        imp_bid = imp.get("impact_bid_price")
        imp_ask = imp.get("impact_ask_price")
        idx = index_data[ts]
        if imp_bid and imp_ask and idx and idx > 0:
            p = (max(0, imp_bid - idx) - max(0, idx - imp_ask)) / idx
            premiums.append(p)

    return premiums


def query_exchange_fr(query_api, exchange: str, symbol: str,
                      bucket: str, lookback: str) -> float | None:
    """Get the latest exchange-reported funding rate from DB."""
    flux = f'''
from(bucket: "{bucket}")
  |> range(start: {lookback})
  |> filter(fn: (r) => r._measurement == "index_price")
  |> filter(fn: (r) => r.exchange == "{exchange}")
  |> filter(fn: (r) => r.symbol == "{symbol}")
  |> filter(fn: (r) => r._field == "funding_rate")
  |> last()
'''
    for table in query_api.query(flux):
        for r in table.records:
            return r.get_value()
    return None


# ============================================================
# Per-exchange calculation
# ============================================================

def calc_for_exchange(query_api, exchange: str, symbol: str,
                      profile: dict, params: dict, bucket: str) -> dict | None:
    """Calculate funding rate for one exchange using config-driven rules."""

    interval_hours = params.get("funding_interval_hours", 1)
    sample_sec = profile.get("sampling_interval_sec", 5)
    sample_window = f"{sample_sec}s"
    lookback = f"-{interval_hours}h"

    # Choose averaging method
    if interval_hours <= 1:
        avg_method_key = profile.get("avg_method_1h", "equal")
    else:
        avg_method_key = profile.get("avg_method_gt1h", "time_weighted")
    avg_func = time_weighted_avg if avg_method_key == "time_weighted" else equal_avg

    # Choose FR formula
    fr_formula_key = profile.get("fr_formula", "binance")
    fr_func = FR_FUNCS.get(fr_formula_key, fr_binance)

    # Query data
    premiums = query_premium_samples(query_api, exchange, symbol, bucket,
                                     sample_window, lookback)
    if not premiums:
        print(f"  {exchange}: no premium samples")
        return None

    exch_fr = query_exchange_fr(query_api, exchange, symbol, bucket, lookback)

    # Calculate
    avg_premium = avg_func(premiums)
    interest_rate = params.get("interest_rate", 0.0001)
    cap = params.get("funding_rate_cap")
    floor = params.get("funding_rate_floor")

    # For Binance/Aster: if no explicit cap/floor from API, derive from maint_margin_ratio
    if cap is None and profile.get("cap_floor_source") == "api_or_maint_margin":
        mmr = params.get("maint_margin_ratio")
        factor = profile.get("cap_floor_factor", 0.75)
        if mmr:
            cap = factor * mmr
            floor = -cap

    calc_fr = fr_func(avg_premium, interest_rate, interval_hours, cap, floor)

    avg_method_label = "tw" if avg_method_key == "time_weighted" else "eq"
    print(f"  {exchange}: samples={len(premiums)}, sample={sample_window}, "
          f"avg({avg_method_label})={avg_premium*100:.6f}%, "
          f"IR={interest_rate*100:.6f}%, "
          f"formula={fr_formula_key}, "
          f"calc_fr={calc_fr*100:.6f}% ({calc_fr:.8f}), "
          f"exch_fr={exch_fr if exch_fr else 'N/A'}")

    return {
        "exchange": exchange,
        "avg_premium": round(avg_premium, 10),
        "calc_funding_rate": round(calc_fr, 10),
        "exchange_funding_rate": exch_fr,
        "sample_count": len(premiums),
    }


# ============================================================
# Main
# ============================================================

def main():
    config = load_config()
    influx_cfg = config["influxdb"]
    bucket = influx_cfg["bucket"]

    client = InfluxDBClient(url=influx_cfg["url"], token=influx_cfg["token"],
                            org=influx_cfg["org"])
    query_api = client.query_api()
    write_api = client.write_api(write_options=SYNCHRONOUS)

    profiles = config.get("platform_profiles", {})
    all_params = config.get("symbol_params", {})
    now = datetime.now(timezone.utc)

    print(f"Calculating funding rates at {now.isoformat()}")
    print()

    for pair in config.get("pairs", []):
        symbol = pair["symbol"]
        for exchange in pair.get("exchanges", []):
            profile = profiles.get(exchange, profiles.get("binance", {}))
            params = all_params.get(symbol, {}).get(exchange, {})

            result = calc_for_exchange(query_api, exchange, symbol,
                                       profile, params, bucket)
            if result is None:
                continue

            point = (
                Point("calculated_funding_rate")
                .tag("exchange", exchange)
                .tag("symbol", symbol)
                .field("avg_premium", result["avg_premium"])
                .field("calc_funding_rate", result["calc_funding_rate"])
                .field("sample_count", result["sample_count"])
                .time(now, WritePrecision.S)
            )
            if result["exchange_funding_rate"] is not None:
                point = point.field("exchange_funding_rate",
                                    result["exchange_funding_rate"])

            write_api.write(bucket=bucket, record=point)
            print(f"  -> written to InfluxDB")

    print()
    print("Done.")
    client.close()


if __name__ == "__main__":
    main()
