import { useEffect, useRef, useState } from 'react'

export interface Ticker {
  bid: number
  ask: number
  timestamp: number
}

export interface OrderBookData {
  exchange: string
  symbol: string
  bids: { price: number; quantity: number }[]
  asks: { price: number; quantity: number }[]
}

export interface FundingRate {
  exchange: string
  symbol: string
  rate: number
  next_funding_time: number
  mark_price: number
  index_price: number
}

// --- WebSocket stream per exchange ---

function toBinanceStream(symbol: string): string {
  return symbol.toLowerCase()
}

function toOkxInstId(symbol: string): string {
  const base = symbol.replace(/USDT$/, '')
  return `${base}-USDT-SWAP`
}

type Listener = (msg: any) => void

interface SharedWS {
  ws: WebSocket | null
  listeners: Set<Listener>
  refCount: number
  reconnectTimer?: number
}

const sharedConns = new Map<string, SharedWS>()

function getWSUrl(exchange: string): string {
  switch (exchange) {
    case 'binance': return 'wss://fstream.binance.com/ws'
    case 'aster': return 'wss://fstream.asterdex.com/ws'
    case 'okx': return 'wss://ws.okx.com:8443/ws/v5/public'
    default: return ''
  }
}

function getSubscribeMessages(exchange: string, symbol: string): any[] {
  switch (exchange) {
    case 'binance':
    case 'aster':
      return [
        { method: 'SUBSCRIBE', params: [`${toBinanceStream(symbol)}@bookTicker`], id: 1 },
        { method: 'SUBSCRIBE', params: [`${toBinanceStream(symbol)}@depth5@100ms`], id: 2 },
      ]
    case 'okx':
      return [
        { op: 'subscribe', args: [{ channel: 'tickers', instId: toOkxInstId(symbol) }] },
        { op: 'subscribe', args: [{ channel: 'books5', instId: toOkxInstId(symbol) }] },
      ]
    default:
      return []
  }
}

function connectShared(key: string, exchange: string, symbol: string) {
  const shared = sharedConns.get(key)
  if (!shared) return

  const url = getWSUrl(exchange)
  if (!url) return

  const ws = new WebSocket(url)
  shared.ws = ws

  ws.onopen = () => {
    for (const msg of getSubscribeMessages(exchange, symbol)) {
      ws.send(JSON.stringify(msg))
    }
  }

  ws.onmessage = (e) => {
    try {
      const msg = JSON.parse(e.data)
      for (const listener of shared.listeners) {
        listener(msg)
      }
    } catch { /* ignore */ }
  }

  ws.onclose = () => {
    shared.ws = null
    if (shared.refCount > 0) {
      shared.reconnectTimer = window.setTimeout(() => connectShared(key, exchange, symbol), 3000)
    }
  }

  ws.onerror = () => ws.close()
}

function subscribeShared(exchange: string, symbol: string, listener: Listener): () => void {
  const key = `${exchange}:${symbol}`

  let shared = sharedConns.get(key)
  if (!shared) {
    shared = { ws: null, listeners: new Set(), refCount: 0 }
    sharedConns.set(key, shared)
  }

  shared.listeners.add(listener)
  shared.refCount++

  if (!shared.ws) {
    connectShared(key, exchange, symbol)
  }

  return () => {
    shared!.listeners.delete(listener)
    shared!.refCount--
    if (shared!.refCount <= 0) {
      clearTimeout(shared!.reconnectTimer)
      shared!.ws?.close()
      shared!.ws = null
      sharedConns.delete(key)
    }
  }
}

// --- Hooks ---

function parseBinanceTicker(msg: any, symbol: string): Ticker | null {
  if (msg.e === 'bookTicker' && msg.s === symbol) {
    return { bid: parseFloat(msg.b), ask: parseFloat(msg.a), timestamp: msg.T || Date.now() }
  }
  return null
}

function parseBinanceBook(msg: any, exchange: string, symbol: string): OrderBookData | null {
  if (msg.e === 'depthUpdate' && msg.s === symbol) {
    return {
      exchange, symbol,
      bids: (msg.b || []).slice(0, 5).map(([p, q]: string[]) => ({ price: parseFloat(p), quantity: parseFloat(q) })),
      asks: (msg.a || []).slice(0, 5).map(([p, q]: string[]) => ({ price: parseFloat(p), quantity: parseFloat(q) })),
    }
  }
  if (msg.lastUpdateId && msg.bids) {
    return {
      exchange, symbol,
      bids: (msg.bids || []).slice(0, 5).map(([p, q]: string[]) => ({ price: parseFloat(p), quantity: parseFloat(q) })),
      asks: (msg.asks || []).slice(0, 5).map(([p, q]: string[]) => ({ price: parseFloat(p), quantity: parseFloat(q) })),
    }
  }
  return null
}

function parseOkxTicker(msg: any, symbol: string): Ticker | null {
  if (msg.arg?.channel === 'tickers' && msg.data?.[0]) {
    const d = msg.data[0]
    if (d.instId === toOkxInstId(symbol)) {
      return { bid: parseFloat(d.bidPx || '0'), ask: parseFloat(d.askPx || '0'), timestamp: parseInt(d.ts || '0') }
    }
  }
  return null
}

function parseOkxBook(msg: any, exchange: string, symbol: string): OrderBookData | null {
  if (msg.arg?.channel === 'books5' && msg.data?.[0]) {
    const d = msg.data[0]
    if (msg.arg.instId === toOkxInstId(symbol)) {
      return {
        exchange, symbol,
        bids: (d.bids || []).slice(0, 5).map(([p, q]: string[]) => ({ price: parseFloat(p), quantity: parseFloat(q) })),
        asks: (d.asks || []).slice(0, 5).map(([p, q]: string[]) => ({ price: parseFloat(p), quantity: parseFloat(q) })),
      }
    }
  }
  return null
}

export function useTicker(exchangeName: string, symbol: string): Ticker | null {
  const [ticker, setTicker] = useState<Ticker | null>(null)
  const tickerRef = useRef(ticker)
  tickerRef.current = ticker

  useEffect(() => {
    setTicker(null)
    const unsub = subscribeShared(exchangeName, symbol, (msg) => {
      let t: Ticker | null = null
      if (exchangeName === 'okx') {
        t = parseOkxTicker(msg, symbol)
      } else {
        t = parseBinanceTicker(msg, symbol)
      }
      if (t) setTicker(t)
    })
    return unsub
  }, [exchangeName, symbol])

  return ticker
}

export function useOrderBook(exchangeName: string, symbol: string): OrderBookData | null {
  const [book, setBook] = useState<OrderBookData | null>(null)

  useEffect(() => {
    setBook(null)
    const unsub = subscribeShared(exchangeName, symbol, (msg) => {
      let b: OrderBookData | null = null
      if (exchangeName === 'okx') {
        b = parseOkxBook(msg, exchangeName, symbol)
      } else {
        b = parseBinanceBook(msg, exchangeName, symbol)
      }
      if (b) setBook(b)
    })
    return unsub
  }, [exchangeName, symbol])

  return book
}

// --- Funding rate: REST poll (no real-time WS stream on all exchanges) ---

const FUNDING_REST: Record<string, { url: (s: string) => string; parse: (d: any, ex: string, sym: string) => FundingRate }> = {
  binance: {
    url: (s) => `https://fapi.binance.com/fapi/v1/premiumIndex?symbol=${s}`,
    parse: (d, ex, sym) => ({
      exchange: ex, symbol: sym,
      rate: parseFloat(d.lastFundingRate || '0'),
      next_funding_time: d.nextFundingTime || 0,
      mark_price: parseFloat(d.markPrice || '0'),
      index_price: parseFloat(d.indexPrice || '0'),
    }),
  },
  aster: {
    url: (s) => `https://fapi.asterdex.com/fapi/v1/premiumIndex?symbol=${s}`,
    parse: (d, ex, sym) => ({
      exchange: ex, symbol: sym,
      rate: parseFloat(d.lastFundingRate || '0'),
      next_funding_time: d.nextFundingTime || 0,
      mark_price: parseFloat(d.markPrice || '0'),
      index_price: parseFloat(d.indexPrice || '0'),
    }),
  },
  okx: {
    url: (s) => `https://www.okx.com/api/v5/public/funding-rate?instId=${toOkxInstId(s)}`,
    parse: (d, ex, sym) => {
      const fr = d.data?.[0] || {}
      return {
        exchange: ex, symbol: sym,
        rate: parseFloat(fr.fundingRate || '0'),
        next_funding_time: parseInt(fr.nextFundingTime || '0'),
        mark_price: 0,
        index_price: 0,
      }
    },
  },
}

async function fetchJSON(url: string): Promise<any> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${res.status}`)
  return res.json()
}

export function useFundingRate(exchangeName: string, symbol: string) {
  const [funding, setFunding] = useState<FundingRate | null>(null)

  useEffect(() => {
    const cfg = FUNDING_REST[exchangeName]
    if (!cfg) return
    let cancelled = false

    const poll = async () => {
      try {
        const data = await fetchJSON(cfg.url(symbol))
        if (!cancelled) setFunding(cfg.parse(data, exchangeName, symbol))
      } catch { /* ignore */ }
    }

    setFunding(null)
    poll()
    const iv = setInterval(poll, 30000)
    return () => { cancelled = true; clearInterval(iv) }
  }, [exchangeName, symbol])

  return funding
}
