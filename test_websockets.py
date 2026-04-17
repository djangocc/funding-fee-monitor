#!/usr/bin/env python3
"""Test WebSocket endpoints for Binance, AsterDEX, and OKX."""

import asyncio
import json
import websockets
import ssl

async def test_binance():
    """Test Binance Futures WebSocket."""
    print("\n" + "="*60)
    print("Testing Binance Futures WebSocket")
    print("="*60)

    uri = "wss://fstream.binance.com/ws/raveusdt@markPrice@1s"
    try:
        async with websockets.connect(uri) as websocket:
            print(f"✓ Connected to: {uri}")

            # Receive 3 messages
            for i in range(3):
                message = await asyncio.wait_for(websocket.recv(), timeout=5.0)
                data = json.loads(message)
                print(f"\nMessage {i+1}:")
                print(json.dumps(data, indent=2))

            return True
    except Exception as e:
        print(f"✗ Error: {e}")
        return False

async def test_asterdex():
    """Test AsterDEX WebSocket."""
    print("\n" + "="*60)
    print("Testing AsterDEX WebSocket")
    print("="*60)

    uri = "wss://fstream.asterdex.com/ws/raveusdt@markPrice@1s"

    # Try with SSL verification disabled
    ssl_context = ssl.create_default_context()
    ssl_context.check_hostname = False
    ssl_context.verify_mode = ssl.CERT_NONE

    try:
        async with websockets.connect(uri, ssl=ssl_context) as websocket:
            print(f"✓ Connected to: {uri}")

            # Receive 3 messages
            for i in range(3):
                message = await asyncio.wait_for(websocket.recv(), timeout=5.0)
                data = json.loads(message)
                print(f"\nMessage {i+1}:")
                print(json.dumps(data, indent=2))

            return True
    except Exception as e:
        print(f"✗ Error: {e}")
        return False

async def test_okx():
    """Test OKX WebSocket."""
    print("\n" + "="*60)
    print("Testing OKX WebSocket")
    print("="*60)

    uri = "wss://ws.okx.com:8443/ws/v5/public"
    try:
        async with websockets.connect(uri) as websocket:
            print(f"✓ Connected to: {uri}")

            # Subscribe to tickers channel
            subscribe_msg = {
                "op": "subscribe",
                "args": [{"channel": "tickers", "instId": "RAVE-USDT-SWAP"}]
            }
            await websocket.send(json.dumps(subscribe_msg))
            print(f"\n→ Sent subscription: {json.dumps(subscribe_msg)}")

            # Receive messages (subscription confirmation + data)
            for i in range(5):
                message = await asyncio.wait_for(websocket.recv(), timeout=10.0)
                data = json.loads(message)
                print(f"\nMessage {i+1}:")
                print(json.dumps(data, indent=2))

                # Check if it's a data message with 'last' field
                if 'data' in data and data['data']:
                    if 'last' in data['data'][0]:
                        print(f"✓ Found 'last' price field: {data['data'][0]['last']}")

            return True
    except Exception as e:
        print(f"✗ Error: {e}")
        return False

async def main():
    """Run all tests."""
    print("WebSocket Endpoint Testing")
    print("Testing connections and message formats...")

    results = {
        'Binance': await test_binance(),
        'AsterDEX': await test_asterdex(),
        'OKX': await test_okx()
    }

    print("\n" + "="*60)
    print("Summary")
    print("="*60)
    for exchange, success in results.items():
        status = "✓ SUCCESS" if success else "✗ FAILED"
        print(f"{exchange}: {status}")

if __name__ == "__main__":
    asyncio.run(main())
