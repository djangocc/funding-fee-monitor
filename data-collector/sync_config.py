"""
Sync dynamic exchange parameters into config.json.

Fetches from each exchange's public API:
  - max_leverage, impact_notional (= 200 * max_leverage)
  - funding_interval_hours
  - interest_rate
  - funding_rate_cap / funding_rate_floor
  - maint_margin_ratio (Binance/Aster only)

Run standalone or import sync_all() from collector.
"""

import json
import os
import time
import traceback
from datetime import datetime, timezone

import requests
import urllib3

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

BINANCE_FAPI = "https://fapi.binance.com"
ASTER_FAPI = "https://fapi.asterdex.com"
OKX_API = "https://www.okx.com"

# Aster uses self-signed certs
SSL_VERIFY = {
    "binance": True,
    "aster": False,
    "okx": False,  # OKX sometimes has cert issues in some networks
}


def _get(url, params=None, verify=True, timeout=10):
    resp = requests.get(url, params=params, verify=verify, timeout=timeout)
    resp.raise_for_status()
    return resp.json()


def _symbol_to_okx(symbol: str) -> str:
    base = symbol.replace("USDT", "")
    return f"{base}-USDT-SWAP"


# ============================================================
# Binance / Aster (Binance-compatible API)
# ============================================================

def sync_binance_like(symbol: str, base_url: str, verify: bool) -> dict:
    """
    Sync params from a Binance-compatible API (Binance or Aster).

    Sources:
      GET /fapi/v1/exchangeInfo        → max_leverage (from requiredMarginPercent), maint_margin_ratio
      GET /fapi/v1/fundingInfo          → funding_interval_hours, adjustedFundingRateCap/Floor
      GET /fapi/v1/premiumIndex?symbol= → interest_rate
    """
    result = {}

    # --- exchangeInfo ---
    try:
        data = _get(f"{base_url}/fapi/v1/exchangeInfo", verify=verify)
        for s in data.get("symbols", []):
            if s["symbol"] == symbol:
                req_margin = float(s.get("requiredMarginPercent", 0))
                maint_margin = float(s.get("maintMarginPercent", 0))
                if req_margin > 0:
                    max_lev = round(100 / req_margin)
                    result["max_leverage"] = max_lev
                    result["impact_notional"] = 200 * max_lev
                if maint_margin > 0:
                    result["maint_margin_ratio"] = round(maint_margin / 100, 6)
                break
    except Exception:
        traceback.print_exc()

    # --- fundingInfo ---
    try:
        data = _get(f"{base_url}/fapi/v1/fundingInfo", verify=verify)
        for item in data:
            if item.get("symbol") == symbol:
                if item.get("fundingIntervalHours") is not None:
                    result["funding_interval_hours"] = int(item["fundingIntervalHours"])
                cap = item.get("adjustedFundingRateCap")
                floor = item.get("adjustedFundingRateFloor")
                if cap is not None:
                    result["funding_rate_cap"] = float(cap)
                if floor is not None:
                    result["funding_rate_floor"] = float(floor)
                break
    except Exception:
        traceback.print_exc()

    # --- premiumIndex ---
    try:
        data = _get(f"{base_url}/fapi/v1/premiumIndex", params={"symbol": symbol}, verify=verify)
        if isinstance(data, list):
            data = data[0]
        ir = data.get("interestRate")
        if ir is not None:
            result["interest_rate"] = float(ir)
    except Exception:
        traceback.print_exc()

    return result


# ============================================================
# OKX
# ============================================================

def sync_okx(symbol: str) -> dict:
    """
    Sync params from OKX API.

    Sources:
      GET /api/v5/public/instruments?instType=SWAP&instId=  → max_leverage
      GET /api/v5/public/funding-rate?instId=               → interest_rate, cap, floor, interval, impactValue
    """
    inst_id = _symbol_to_okx(symbol)
    result = {}
    verify = SSL_VERIFY["okx"]

    # --- instruments ---
    try:
        data = _get(f"{OKX_API}/api/v5/public/instruments",
                     params={"instType": "SWAP", "instId": inst_id}, verify=verify)
        inst = data["data"][0]
        max_lev = int(inst["lever"])
        result["max_leverage"] = max_lev
        result["impact_notional"] = 200 * max_lev
    except Exception:
        traceback.print_exc()

    # --- funding-rate ---
    try:
        data = _get(f"{OKX_API}/api/v5/public/funding-rate",
                     params={"instId": inst_id}, verify=verify)
        fr = data["data"][0]

        ir = fr.get("interestRate")
        if ir is not None:
            result["interest_rate"] = float(ir)

        cap = fr.get("maxFundingRate")
        if cap is not None:
            result["funding_rate_cap"] = float(cap)

        floor = fr.get("minFundingRate")
        if floor is not None:
            result["funding_rate_floor"] = float(floor)

        impact_val = fr.get("impactValue")
        if impact_val is not None:
            result["impact_notional"] = float(impact_val)

        # Derive funding_interval_hours from timestamps
        ft = int(fr.get("fundingTime", 0))
        nft = int(fr.get("nextFundingTime", 0))
        if ft > 0 and nft > 0 and nft > ft:
            result["funding_interval_hours"] = round((nft - ft) / 3600000)
    except Exception:
        traceback.print_exc()

    return result


# ============================================================
# Main sync logic
# ============================================================

SYNC_FUNCS = {
    "binance": lambda sym: sync_binance_like(sym, BINANCE_FAPI, SSL_VERIFY["binance"]),
    "aster": lambda sym: sync_binance_like(sym, ASTER_FAPI, SSL_VERIFY["aster"]),
    "okx": lambda sym: sync_okx(sym),
}


def sync_all(config_path: str = "config.json") -> dict:
    """
    Read config.json, sync all symbol_params from exchange APIs, write back.
    Returns the updated config.
    """
    with open(config_path) as f:
        config = json.load(f)

    pairs = config.get("pairs", [])
    symbol_params = config.setdefault("symbol_params", {})
    now_iso = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

    for pair in pairs:
        symbol = pair["symbol"]
        symbol_params.setdefault(symbol, {})

        for exchange in pair.get("exchanges", []):
            sync_func = SYNC_FUNCS.get(exchange)
            if not sync_func:
                print(f"  [sync] No sync function for {exchange}, skipping")
                continue

            print(f"  [sync] {exchange}/{symbol}...", end=" ", flush=True)
            try:
                updates = sync_func(symbol)
                if updates:
                    existing = symbol_params[symbol].setdefault(exchange, {})
                    existing.update(updates)
                    existing["_last_synced"] = now_iso
                    print(f"OK ({len(updates)} fields)")
                else:
                    print("no data returned")
            except Exception:
                print("FAILED")
                traceback.print_exc()

    with open(config_path, "w") as f:
        json.dump(config, f, indent=2)
        f.write("\n")

    print(f"  [sync] Config saved to {config_path}")
    return config


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    print(f"Syncing config at {datetime.now(timezone.utc).isoformat()}")
    sync_all()
    print("Done.")
