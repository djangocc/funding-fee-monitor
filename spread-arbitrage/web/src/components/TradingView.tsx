import { useState, useEffect, useCallback, useRef } from 'react'
import { useWebSocket, type WSEvent } from '../hooks/useWebSocket'
import { useToast, playSound } from '../hooks/useToast'
import { OrderBook } from './OrderBook'
import { ToastContainer } from './ToastContainer'
import * as api from '../api'

function latencyColor(ms: number | null): string {
  if (ms === null) return 'var(--accent-green)'
  if (ms < 500) return 'var(--accent-green)'
  if (ms < 2000) return 'var(--accent-amber)'
  return 'var(--accent-red)'
}

const EXCHANGE_COLORS: Record<string, string> = {
  binance: '#f0b90b',
  aster: '#8b5cf6',
  okx: '#00c8ff',
}

interface Quote {
  bid: number
  ask: number
  timestamp: string
}

interface SpreadUpdate {
  spread: number
  open_counter: number
  close_counter: number
  bid_a: number
  ask_a: number
  bid_b: number
  ask_b: number
}

interface OrderBookData {
  exchange: string
  symbol: string
  bids: { price: number; quantity: number }[]
  asks: { price: number; quantity: number }[]
}

interface TradingViewProps {
  symbol: string
  exchangeA: string
  exchangeB: string
}

export function TradingView({ symbol, exchangeA, exchangeB }: TradingViewProps) {
  const [quoteA, setQuoteA] = useState<Quote | null>(null)
  const [quoteB, setQuoteB] = useState<Quote | null>(null)
  const [bookA, setBookA] = useState<OrderBookData | null>(null)
  const [fundingA, setFundingA] = useState<api.FundingRate | null>(null)
  const [fundingB, setFundingB] = useState<api.FundingRate | null>(null)
  const [bookB, setBookB] = useState<OrderBookData | null>(null)
  const [tasks, setTasks] = useState<api.Task[]>([])
  const [spreads, setSpreads] = useState<Map<string, SpreadUpdate>>(new Map())
  const [positions, setPositions] = useState<api.Position[]>([])
  const [tradesA, setTradesA] = useState<api.Order[]>([])
  const [tradesB, setTradesB] = useState<api.Order[]>([])
  const [showCreatePanel, setShowCreatePanel] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const subscribed = useRef(false)
  const { toasts, addToast, removeToast } = useToast()

  // Task form state
  const [direction, setDirection] = useState<'long_spread' | 'short_spread'>('short_spread')
  const [openThreshold, setOpenThreshold] = useState('0.5')
  const [closeThreshold, setCloseThreshold] = useState('0.1')
  const [confirmCount, setConfirmCount] = useState('3')
  const [qtyPerOrder, setQtyPerOrder] = useState('1')
  const [maxPositionQty, setMaxPositionQty] = useState('3')
  const [latencyMs, setLatencyMs] = useState('50')

  // Subscribe to market data on mount
  useEffect(() => {
    if (!subscribed.current) {
      subscribed.current = true
      api.subscribe(exchangeA, symbol).catch(e => console.warn('subscribe A:', e))
      api.subscribe(exchangeB, symbol).catch(e => console.warn('subscribe B:', e))
    }
    return () => {
      api.unsubscribe(exchangeA, symbol).catch(() => {})
      api.unsubscribe(exchangeB, symbol).catch(() => {})
      subscribed.current = false
    }
  }, [exchangeA, exchangeB, symbol])

  const refreshTasks = useCallback(async () => {
    try {
      const all = await api.listTasks()
      setTasks(all.filter(t => t.symbol === symbol && t.exchange_a === exchangeA && t.exchange_b === exchangeB))
    } catch { /* ignore */ }
  }, [symbol, exchangeA, exchangeB])

  const refreshPositions = useCallback(async () => {
    try {
      const [posA, posB] = await Promise.all([
        api.getPositionByExchange(exchangeA, symbol),
        api.getPositionByExchange(exchangeB, symbol),
      ])
      setPositions([posA, posB])
    } catch { /* ignore */ }
  }, [exchangeA, exchangeB, symbol])

  const refreshTrades = useCallback(async () => {
    try { setTradesA(await api.getOrdersByExchange(exchangeA, symbol)) } catch { /* ignore */ }
    try { setTradesB(await api.getOrdersByExchange(exchangeB, symbol)) } catch { /* ignore */ }
  }, [exchangeA, exchangeB, symbol])

  const handleWS = useCallback((event: WSEvent) => {
    if (event.type === 'quote') {
      const d = event.data
      if (d.symbol === symbol || d.symbol === '') {
        if (d.exchange === exchangeA) setQuoteA({ bid: d.bid, ask: d.ask, timestamp: d.timestamp })
        if (d.exchange === exchangeB) setQuoteB({ bid: d.bid, ask: d.ask, timestamp: d.timestamp })
      }
    }
    if (event.type === 'orderbook') {
      const d = event.data as OrderBookData
      if (d.exchange === exchangeA) setBookA(d)
      if (d.exchange === exchangeB) setBookB(d)
    }
    if (event.type === 'spread_update') {
      setSpreads(prev => new Map(prev).set(event.task_id, event.data))
    }
    if (event.type === 'trade_executed') {
      const d = event.data as any
      const action = d.action === 'open' ? '开仓' : '平仓'
      const a = d.order_a
      const b = d.order_b
      const fmtOrder = (o: any) => {
        if (!o) return ''
        const qty = o.quantity > 0 ? o.quantity : '?'
        const price = o.price > 0 ? `@${o.price.toFixed(4)}` : '市价成交'
        return `${o.exchange?.toUpperCase()} ${o.side} ${qty}个 ${price}`
      }
      addToast(`${action}成功！\n${fmtOrder(a)}\n${fmtOrder(b)}`, 'success', 8000)
      playSound(d.action === 'open' ? 'open' : 'close')
      refreshTasks()
      refreshPositions()
      refreshTrades()
    }
    if (event.type === 'task_status') {
      refreshTasks()
    }
    if (event.type === 'error') {
      const d = event.data as any
      const severity = d.severity || 'error'
      const msg = d.error || JSON.stringify(d)
      addToast(msg, severity === 'critical' ? 'error' : 'warning', 10000)
      playSound('error')
      setError(msg)
      refreshTasks()
    }
  }, [symbol, exchangeA, exchangeB, refreshTasks, refreshPositions])

  const { connected, latencyMs: wsLatency } = useWebSocket(handleWS)

  useEffect(() => { refreshTasks() }, [refreshTasks])
  useEffect(() => { refreshPositions() }, [refreshPositions])
  useEffect(() => { refreshTrades() }, [refreshTrades])

  // Fetch funding rates
  const refreshFunding = useCallback(async () => {
    try { setFundingA(await api.getFundingRate(exchangeA, symbol)) } catch { /* ignore */ }
    try { setFundingB(await api.getFundingRate(exchangeB, symbol)) } catch { /* ignore */ }
  }, [exchangeA, exchangeB, symbol])

  useEffect(() => { refreshFunding() }, [refreshFunding])

  // Auto-refresh positions every 15s, funding every 30s
  useEffect(() => {
    const iv = setInterval(() => { refreshPositions(); refreshTrades() }, 15000)
    const fiv = setInterval(refreshFunding, 30000)
    return () => { clearInterval(iv); clearInterval(fiv) }
  }, [refreshPositions, refreshTrades, refreshFunding])

  const spread = (quoteA && quoteB) ? (quoteA.bid - quoteB.ask) : null
  const midPrice = (quoteA && quoteB) ? (quoteA.bid + quoteB.ask) / 2 : null
  const spreadPct = (spread !== null && midPrice && midPrice !== 0) ? (spread / midPrice * 100) : null

  const handleCreateTask = async () => {
    try {
      setError(null)
      await api.createTask({
        symbol, exchange_a: exchangeA, exchange_b: exchangeB, direction,
        open_threshold: parseFloat(openThreshold),
        close_threshold: parseFloat(closeThreshold),
        confirm_count: parseInt(confirmCount),
        quantity_per_order: parseFloat(qtyPerOrder),
        max_position_qty: parseFloat(maxPositionQty),
        data_max_latency_ms: parseInt(latencyMs),
      })
      setShowCreatePanel(false)
      refreshTasks()
    } catch (e: any) { setError(e.message) }
  }

  const colorA = EXCHANGE_COLORS[exchangeA] || 'var(--accent-blue)'
  const colorB = EXCHANGE_COLORS[exchangeB] || 'var(--accent-purple)'

  const inputCss: React.CSSProperties = {
    width: '100%', padding: '9px 12px',
    background: 'var(--bg-input)', border: '1px solid var(--border)',
    borderRadius: 8, color: 'var(--text-primary)',
    fontFamily: 'var(--font-mono)', fontSize: 13, outline: 'none',
  }
  const labelCss: React.CSSProperties = {
    display: 'block', fontSize: 11, fontWeight: 600, color: 'var(--text-dim)',
    textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 2,
  }
  const hintCss: React.CSSProperties = {
    fontSize: 11, color: 'var(--text-dim)', lineHeight: 1.5, marginBottom: 6,
    fontFamily: 'var(--font-display)', opacity: 0.7,
  }

  return (
    <div style={{ minHeight: '100vh', background: 'var(--bg-deep)' }}>
      <ToastContainer toasts={toasts} onRemove={removeToast} />
      {/* Header */}
      <header style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '12px 24px', borderBottom: '1px solid var(--border)', background: 'var(--bg-panel)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          <a href="/" style={{
            color: 'var(--text-secondary)', textDecoration: 'none', fontSize: 18, padding: '4px 8px',
          }}>&larr;</a>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 18, fontWeight: 700 }}>{symbol}</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span style={{ fontSize: 12, fontWeight: 600, color: colorA, background: `${colorA}15`, padding: '3px 10px', borderRadius: 6 }}>
              {exchangeA.toUpperCase()}
            </span>
            <a
              href={`/trade/${symbol}/${exchangeB}/${exchangeA}`}
              style={{
                color: 'var(--text-dim)', fontSize: 14, textDecoration: 'none',
                padding: '2px 4px', borderRadius: 4, cursor: 'pointer',
                transition: 'color 0.15s',
              }}
              onMouseEnter={e => (e.currentTarget.style.color = 'var(--accent-blue)')}
              onMouseLeave={e => (e.currentTarget.style.color = 'var(--text-dim)')}
              title="交换 A / B"
            >⇄</a>
            <span style={{ fontSize: 12, fontWeight: 600, color: colorB, background: `${colorB}15`, padding: '3px 10px', borderRadius: 6 }}>
              {exchangeB.toUpperCase()}
            </span>
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{
            width: 7, height: 7, borderRadius: '50%',
            background: connected ? latencyColor(wsLatency) : 'var(--accent-red)',
            boxShadow: connected ? `0 0 8px ${latencyColor(wsLatency)}` : 'none',
            display: 'inline-block',
          }} />
          <span style={{ fontSize: 12, color: connected ? latencyColor(wsLatency) : 'var(--accent-red)', fontFamily: 'var(--font-mono)' }}>
            {connected
              ? wsLatency !== null ? `${wsLatency}ms` : 'LIVE'
              : 'OFFLINE'}
          </span>
        </div>
      </header>

      <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
        {/* Error */}
        {error && (
          <div style={{
            background: 'rgba(255,71,87,0.1)', border: '1px solid rgba(255,71,87,0.3)',
            borderRadius: 10, padding: '10px 16px', marginBottom: 16,
            display: 'flex', justifyContent: 'space-between', alignItems: 'center',
            fontSize: 13, color: 'var(--accent-red)',
          }}>
            <span>{error}</span>
            <button onClick={() => setError(null)} style={{ background: 'none', border: 'none', color: 'var(--accent-red)', cursor: 'pointer', fontSize: 16 }}>&times;</button>
          </div>
        )}

        {/* Order Books + Spread */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr auto 1fr', gap: 12, marginBottom: 24, animation: 'fadeIn 0.3s ease-out' }}>
          <OrderBook data={bookA} label={exchangeA.toUpperCase()} color={colorA} />

          {/* Spread center column */}
          <div style={{
            display: 'flex', flexDirection: 'column', justifyContent: 'center', alignItems: 'center',
            minWidth: 140, gap: 8,
          }}>
            <div style={{
              background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 12,
              padding: '16px 20px', textAlign: 'center', width: '100%',
            }}>
              <div style={{ fontSize: 10, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 6 }}>
                Spread
              </div>
              <div style={{
                fontSize: 20, fontWeight: 700, fontFamily: 'var(--font-mono)',
                color: spread !== null ? (spread >= 0 ? 'var(--accent-green)' : 'var(--accent-red)') : 'var(--text-dim)',
              }}>
                {spread !== null ? spread.toFixed(6) : '—'}
              </div>
              <div style={{
                fontSize: 14, fontWeight: 600, fontFamily: 'var(--font-mono)', marginTop: 2,
                color: spreadPct !== null ? (spreadPct >= 0 ? 'var(--accent-green)' : 'var(--accent-red)') : 'var(--text-dim)',
                opacity: 0.8,
              }}>
                {spreadPct !== null ? `${spreadPct >= 0 ? '+' : ''}${spreadPct.toFixed(4)}%` : ''}
              </div>
              <div style={{ fontSize: 10, color: 'var(--text-dim)', marginTop: 4 }}>
                {exchangeA.toUpperCase()} bid − {exchangeB.toUpperCase()} ask
              </div>
            </div>
          </div>

          <OrderBook data={bookB} label={exchangeB.toUpperCase()} color={colorB} />
        </div>

        {/* Funding Rates */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 24 }}>
          <FundingCard data={fundingA} label={exchangeA.toUpperCase()} color={colorA} />
          <FundingCard data={fundingB} label={exchangeB.toUpperCase()} color={colorB} />
        </div>

        {/* Main grid */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 24 }}>
          {/* Tasks */}
          <Panel
            title="Tasks"
            action={tasks.length === 0 || showCreatePanel ? (
              <button onClick={() => setShowCreatePanel(!showCreatePanel)} style={{
                background: showCreatePanel ? 'var(--bg-hover)' : 'var(--accent-blue)',
                color: showCreatePanel ? 'var(--text-secondary)' : '#fff',
                border: 'none', borderRadius: 6, padding: '5px 12px',
                fontSize: 12, fontWeight: 600, cursor: 'pointer',
              }}>
                {showCreatePanel ? 'Cancel' : '+ New'}
              </button>
            ) : (
              <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>同一币对仅限一个任务</span>
            )}
          >
            {showCreatePanel && (
              <div style={{ padding: 18, borderBottom: '1px solid var(--border)', background: 'var(--bg-card)', animation: 'fadeIn 0.2s ease-out' }}>
                {/* Direction */}
                <div style={{ marginBottom: 14 }}>
                  <label style={labelCss}>策略方向</label>
                  <select value={direction} onChange={e => setDirection(e.target.value as any)} style={{ ...inputCss, fontFamily: 'var(--font-display)' }}>
                    <option value="short_spread">做空价差 — 预期 {exchangeA.toUpperCase()} 相对 {exchangeB.toUpperCase()} 会变便宜</option>
                    <option value="long_spread">做多价差 — 预期 {exchangeA.toUpperCase()} 相对 {exchangeB.toUpperCase()} 会变贵</option>
                  </select>
                </div>

                {/* Action summary box */}
                <div style={{
                  background: 'var(--bg-deep)', borderRadius: 8, padding: '12px 14px', marginBottom: 14,
                  fontSize: 12, lineHeight: 1.8, border: '1px solid var(--border)',
                }}>
                  <div style={{ fontWeight: 600, color: 'var(--text-primary)', marginBottom: 4 }}>
                    策略说明 — 价差 = {exchangeA.toUpperCase()} 卖一价(bid) − {exchangeB.toUpperCase()} 买一价(ask)
                  </div>
                  {direction === 'short_spread' ? (<>
                    <div>
                      <span style={{ color: 'var(--accent-blue)' }}>开仓：</span>
                      <span style={{ color: 'var(--text-secondary)' }}>
                        当价差 {'>'} <b style={{ color: 'var(--text-primary)' }}>{openThreshold || '?'}</b> 时 → 在 <b style={{ color: colorA }}>{exchangeA.toUpperCase()} 卖出</b> + 在 <b style={{ color: colorB }}>{exchangeB.toUpperCase()} 买入</b>
                      </span>
                    </div>
                    <div>
                      <span style={{ color: 'var(--accent-amber)' }}>平仓：</span>
                      <span style={{ color: 'var(--text-secondary)' }}>
                        当价差 {'<'} <b style={{ color: 'var(--text-primary)' }}>{closeThreshold || '?'}</b> 时 → 在 <b style={{ color: colorA }}>{exchangeA.toUpperCase()} 买入</b> + 在 <b style={{ color: colorB }}>{exchangeB.toUpperCase()} 卖出</b>
                      </span>
                    </div>
                    <div style={{ color: 'var(--text-dim)', fontSize: 11, marginTop: 4 }}>
                      利润来源：开仓时价差大（{'>'}{openThreshold || '?'}），平仓时价差小（{'<'}{closeThreshold || '?'}），赚取差值收敛的利润
                    </div>
                  </>) : (<>
                    <div>
                      <span style={{ color: 'var(--accent-blue)' }}>开仓：</span>
                      <span style={{ color: 'var(--text-secondary)' }}>
                        当价差 {'<'} <b style={{ color: 'var(--text-primary)' }}>{openThreshold || '?'}</b> 时 → 在 <b style={{ color: colorA }}>{exchangeA.toUpperCase()} 买入</b> + 在 <b style={{ color: colorB }}>{exchangeB.toUpperCase()} 卖出</b>
                      </span>
                    </div>
                    <div>
                      <span style={{ color: 'var(--accent-amber)' }}>平仓：</span>
                      <span style={{ color: 'var(--text-secondary)' }}>
                        当价差 {'>'} <b style={{ color: 'var(--text-primary)' }}>{closeThreshold || '?'}</b> 时 → 在 <b style={{ color: colorA }}>{exchangeA.toUpperCase()} 卖出</b> + 在 <b style={{ color: colorB }}>{exchangeB.toUpperCase()} 买入</b>
                      </span>
                    </div>
                    <div style={{ color: 'var(--text-dim)', fontSize: 11, marginTop: 4 }}>
                      利润来源：开仓时价差小（{'<'}{openThreshold || '?'}），平仓时价差大（{'>'}{closeThreshold || '?'}），赚取差值扩大的利润
                    </div>
                  </>)}
                </div>

                {/* Thresholds */}
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 14 }}>
                  <div>
                    <label style={labelCss}>开仓阈值</label>
                    <div style={hintCss}>
                      {direction === 'short_spread'
                        ? `价差（${exchangeA.toUpperCase()} bid − ${exchangeB.toUpperCase()} ask）> ${openThreshold || '?'} 时触发开仓`
                        : `价差（${exchangeA.toUpperCase()} ask − ${exchangeB.toUpperCase()} bid）< ${openThreshold || '?'} 时触发开仓`
                      }
                    </div>
                    <input type="number" step="0.001" value={openThreshold} onChange={e => setOpenThreshold(e.target.value)} style={inputCss} />
                  </div>
                  <div>
                    <label style={labelCss}>平仓阈值</label>
                    <div style={hintCss}>
                      {direction === 'short_spread'
                        ? `价差（${exchangeA.toUpperCase()} ask − ${exchangeB.toUpperCase()} bid）< ${closeThreshold || '?'} 时触发平仓。应小于开仓阈值 ${openThreshold || '?'}`
                        : `价差（${exchangeA.toUpperCase()} bid − ${exchangeB.toUpperCase()} ask）> ${closeThreshold || '?'} 时触发平仓。应大于开仓阈值 ${openThreshold || '?'}`
                      }
                    </div>
                    <input type="number" step="0.001" value={closeThreshold} onChange={e => setCloseThreshold(e.target.value)} style={inputCss} />
                  </div>
                </div>

                {/* Confirm count */}
                <div style={{ marginBottom: 14 }}>
                  <label style={labelCss}>连续确认次数</label>
                  <div style={hintCss}>信号需连续出现多少次才真正执行交易，防止瞬间波动误触发。设为 1 表示立即执行</div>
                  <input type="number" min="1" value={confirmCount} onChange={e => setConfirmCount(e.target.value)} style={inputCss} />
                </div>

                {/* Quantity */}
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 14 }}>
                  <div>
                    <label style={labelCss}>每次下单数量</label>
                    <div style={hintCss}>每次开仓或平仓下单的 {symbol} 数量（不是 USDT 金额）。两个交易所同时各下这个数量</div>
                    <input type="number" step="0.1" value={qtyPerOrder} onChange={e => setQtyPerOrder(e.target.value)} style={inputCss} />
                  </div>
                  <div>
                    <label style={labelCss}>最大持仓上限</label>
                    <div style={hintCss}>单边累计持仓不超过 {maxPositionQty || '?'} 个 {symbol}。达到上限后即使满足开仓条件也不再开仓</div>
                    <input type="number" step="0.1" value={maxPositionQty} onChange={e => setMaxPositionQty(e.target.value)} style={inputCss} />
                  </div>
                </div>

                {/* Latency */}
                <div style={{ marginBottom: 16 }}>
                  <label style={labelCss}>最大行情延迟 (毫秒)</label>
                  <div style={hintCss}>如果从交易所收到的行情数据延迟超过 {latencyMs || '?'}ms，则暂停交易决策，防止用过时价格下单</div>
                  <input type="number" value={latencyMs} onChange={e => setLatencyMs(e.target.value)} style={inputCss} />
                </div>

                <button onClick={handleCreateTask} style={{
                  width: '100%', padding: '10px 0', border: 'none', borderRadius: 8,
                  background: 'var(--accent-blue)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer',
                }}>创建任务</button>
              </div>
            )}
            <div style={{ maxHeight: 400, overflowY: 'auto' }}>
              {tasks.length === 0 ? (
                <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>No tasks yet</div>
              ) : tasks.map(task => (
                <TaskRow key={task.id} task={task} spreadData={spreads.get(task.id)} exchangeA={exchangeA} exchangeB={exchangeB} colorA={colorA} colorB={colorB} onRefresh={() => { refreshTasks(); refreshPositions() }} />
              ))}
            </div>
          </Panel>

          {/* Positions */}
          <Panel title="Positions">
            <div style={{ maxHeight: 400, overflowY: 'auto' }}>
              {positions.filter(p => p.size > 0).length === 0 ? (
                <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>No open positions</div>
              ) : (
                <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                  <thead>
                    <tr style={{ borderBottom: '1px solid var(--border)' }}>
                      {['Exchange', 'Side', 'Size', 'Entry', 'PnL'].map(h => (
                        <th key={h} style={{ padding: '10px 14px', textAlign: 'left', fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.06em' }}>{h}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {positions.filter(p => p.size > 0).map((pos, i) => (
                      <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                        <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 500, color: EXCHANGE_COLORS[pos.exchange] || 'var(--text-primary)' }}>{pos.exchange.toUpperCase()}</td>
                        <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 600, fontFamily: 'var(--font-mono)', color: pos.side === 'LONG' ? 'var(--accent-green)' : pos.side === 'SHORT' ? 'var(--accent-red)' : 'var(--text-dim)' }}>{pos.side || '—'}</td>
                        <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)' }}>{pos.size}</td>
                        <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)' }}>{pos.entry_price.toFixed(4)}</td>
                        <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 600, fontFamily: 'var(--font-mono)', color: pos.unrealized_pnl >= 0 ? 'var(--accent-green)' : 'var(--accent-red)' }}>{pos.unrealized_pnl >= 0 ? '+' : ''}{pos.unrealized_pnl.toFixed(4)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </Panel>
        </div>

        {/* Orders — two columns with trigger_id hover highlight */}
        <OrdersSection
          labelA={exchangeA.toUpperCase()} colorA={colorA} ordersA={tradesA}
          labelB={exchangeB.toUpperCase()} colorB={colorB} ordersB={tradesB}
        />
      </div>
    </div>
  )
}

/* ── Sub-components ── */

function Panel({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div style={{ background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 14, overflow: 'hidden' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
        <span style={{ fontSize: 14, fontWeight: 600 }}>{title}</span>
        {action}
      </div>
      {children}
    </div>
  )
}

function TaskRow({ task, spreadData, exchangeA, exchangeB, colorA, colorB, onRefresh }: {
  task: api.Task; spreadData?: SpreadUpdate; exchangeA: string; exchangeB: string; colorA: string; colorB: string; onRefresh: () => void
}) {
  const isRunning = task.status === 'running'
  const A = exchangeA.toUpperCase()
  const B = exchangeB.toUpperCase()
  const isShort = task.direction === 'short_spread'

  const btn = (label: string, color: string, onClick: () => void) => (
    <button onClick={onClick} style={{
      padding: '4px 10px', border: 'none', borderRadius: 5,
      background: `${color}18`, color, fontSize: 11, fontWeight: 600, cursor: 'pointer',
    }}>{label}</button>
  )

  return (
    <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
      {/* Row 1: direction + status + actions */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-mono)', color: isShort ? 'var(--accent-red)' : 'var(--accent-green)' }}>
            {isShort ? '做空价差' : '做多价差'}
          </span>
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: isRunning ? 'var(--accent-green)' : 'var(--text-dim)', display: 'inline-block', animation: isRunning ? 'pulse 2s infinite' : 'none' }} />
          <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>{isRunning ? '自动运行中' : '已停止'}</span>
        </div>
        <div style={{ display: 'flex', gap: 4 }}>
          {isRunning
            ? btn('停止自动', 'var(--accent-red)', () => api.stopTask(task.id).then(onRefresh))
            : btn('启动自动', 'var(--accent-green)', () => api.startTask(task.id).then(onRefresh))
          }
          {btn('手动开仓', 'var(--accent-blue)', () => api.manualOpen(task.id).then(onRefresh))}
          {btn('手动平仓', 'var(--accent-amber)', () => api.manualClose(task.id).then(onRefresh))}
          {!isRunning && btn('删除', 'var(--accent-red)', () => api.deleteTask(task.id).then(onRefresh))}
        </div>
      </div>

      {/* Row 2: what this task does */}
      <div style={{ fontSize: 11, lineHeight: 1.7, color: 'var(--text-secondary)', marginBottom: 6 }}>
        {isShort ? (
          <>
            <div>开仓：价差 {'>'} <b style={{ color: 'var(--text-primary)' }}>{task.open_threshold}</b> 时 → <span style={{ color: colorA }}>卖{A}</span> + <span style={{ color: colorB }}>买{B}</span></div>
            <div>平仓：价差 {'<'} <b style={{ color: 'var(--text-primary)' }}>{task.close_threshold}</b> 时 → <span style={{ color: colorA }}>买{A}</span> + <span style={{ color: colorB }}>卖{B}</span></div>
          </>
        ) : (
          <>
            <div>开仓：价差 {'<'} <b style={{ color: 'var(--text-primary)' }}>{task.open_threshold}</b> 时 → <span style={{ color: colorA }}>买{A}</span> + <span style={{ color: colorB }}>卖{B}</span></div>
            <div>平仓：价差 {'>'} <b style={{ color: 'var(--text-primary)' }}>{task.close_threshold}</b> 时 → <span style={{ color: colorA }}>卖{A}</span> + <span style={{ color: colorB }}>买{B}</span></div>
          </>
        )}
      </div>

      {/* Row 3: params */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 14px', fontSize: 11, color: 'var(--text-dim)', fontFamily: 'var(--font-mono)' }}>
        <span>连续确认 {task.confirm_count}次</span>
        <span>单笔 {task.quantity_per_order}个</span>
        <span>上限 {task.max_position_qty}个</span>
        <span>当前持仓 {task.current_position ?? 0}个</span>
      </div>

      {/* Row 4: live counters */}
      {spreadData && (
        <div style={{ display: 'flex', gap: 14, fontSize: 11, marginTop: 6, fontFamily: 'var(--font-mono)' }}>
          {(task.current_position ?? 0) < task.max_position_qty && (
            <span style={{ color: 'var(--accent-blue)' }}>
              开仓确认 {spreadData.open_counter}/{task.confirm_count}
            </span>
          )}
          {(task.current_position ?? 0) > 0 && (
            <span style={{ color: 'var(--accent-amber)' }}>
              平仓确认 {spreadData.close_counter}/{task.confirm_count}
            </span>
          )}
          {(task.current_position ?? 0) >= task.max_position_qty && (
            <span style={{ color: 'var(--text-dim)' }}>已达持仓上限，暂停开仓</span>
          )}
          {(task.current_position ?? 0) === 0 && (
            <span style={{ color: 'var(--text-dim)' }}>无持仓，暂停平仓</span>
          )}
        </div>
      )}
    </div>
  )
}

function FundingCard({ data, label, color }: { data: api.FundingRate | null; label: string; color: string }) {
  const ratePercent = data ? (data.rate * 100).toFixed(4) : '—'
  const rateColor = data ? (data.rate >= 0 ? 'var(--accent-green)' : 'var(--accent-red)') : 'var(--text-dim)'
  const nextTime = data?.next_funding_time ? new Date(data.next_funding_time).toLocaleTimeString() : '—'

  return (
    <div style={{
      background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 12,
      padding: '12px 18px', display: 'flex', justifyContent: 'space-between', alignItems: 'center',
    }}>
      <div>
        <div style={{ fontSize: 11, fontWeight: 600, color, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>
          {label} 资金费率
        </div>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
          <span style={{ fontSize: 18, fontWeight: 700, fontFamily: 'var(--font-mono)', color: rateColor }}>
            {ratePercent}%
          </span>
          {data && (
            <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>
              下次结算 {nextTime}
            </span>
          )}
        </div>
      </div>
      {data && (data.mark_price > 0 || data.index_price > 0) && (
        <div style={{ textAlign: 'right', fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)' }}>
          {data.mark_price > 0 && <div>标记价 {data.mark_price.toFixed(4)}</div>}
          {data.index_price > 0 && <div>指数价 {data.index_price.toFixed(4)}</div>}
        </div>
      )}
    </div>
  )
}

// Extract trigger ID from clientOrderId (format: "sa-{8hex}-A/B")
function getTriggerID(clientOrderId: string): string | null {
  if (!clientOrderId || !clientOrderId.startsWith('sa-')) return null
  const parts = clientOrderId.split('-')
  if (parts.length >= 2) return parts[1]
  return null
}

function OrdersSection({ labelA, colorA, ordersA, labelB, colorB, ordersB }: {
  labelA: string; colorA: string; ordersA: api.Order[]
  labelB: string; colorB: string; ordersB: api.Order[]
}) {
  const [hoveredTrigger, setHoveredTrigger] = useState<string | null>(null)
  const [tooltipPos, setTooltipPos] = useState<{ x: number; y: number }>({ x: 0, y: 0 })

  const triggerPrices = (() => {
    const map = new Map<string, { priceA: number; priceB: number }>()
    for (const o of ordersA) {
      const t = getTriggerID(o.client_order_id)
      if (t && o.price > 0) {
        const entry = map.get(t) || { priceA: 0, priceB: 0 }
        entry.priceA = o.price
        map.set(t, entry)
      }
    }
    for (const o of ordersB) {
      const t = getTriggerID(o.client_order_id)
      if (t && o.price > 0) {
        const entry = map.get(t) || { priceA: 0, priceB: 0 }
        entry.priceB = o.price
        map.set(t, entry)
      }
    }
    return map
  })()

  const spreadInfo = hoveredTrigger ? (() => {
    const p = triggerPrices.get(hoveredTrigger)
    if (!p || p.priceA === 0 || p.priceB === 0) return null
    const spread = p.priceA - p.priceB
    const mid = (p.priceA + p.priceB) / 2
    const pct = mid !== 0 ? (spread / mid * 100) : 0
    return { spread, pct, priceA: p.priceA, priceB: p.priceB }
  })() : null

  const handleRowEnter = (trigger: string | null, e: React.MouseEvent) => {
    if (trigger) {
      setHoveredTrigger(trigger)
      setTooltipPos({ x: e.clientX, y: e.clientY })
    }
  }
  const handleRowMove = (e: React.MouseEvent) => {
    setTooltipPos({ x: e.clientX, y: e.clientY })
  }

  const thCss: React.CSSProperties = {
    padding: '8px 12px', textAlign: 'left', fontSize: 11, fontWeight: 600,
    color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em',
  }
  const tdCss: React.CSSProperties = { padding: '7px 12px', fontSize: 12, fontFamily: 'var(--font-mono)' }

  const renderTable = (orders: api.Order[], label: string, color: string) => (
    <Panel title={`${label} Orders`} action={<span style={{ fontSize: 11, color, fontWeight: 600 }}>{orders.length}</span>}>
      <div style={{ maxHeight: 300, overflowY: 'auto' }}>
        {orders.length === 0 ? (
          <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>No orders</div>
        ) : (
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)' }}>
                <th style={thCss}>Time</th>
                <th style={thCss}>Side</th>
                <th style={thCss}>Qty</th>
                <th style={thCss}>Price</th>
                <th style={thCss}>Status</th>
              </tr>
            </thead>
            <tbody>
              {orders.slice(0, 30).map((o, i) => {
                const trigger = getTriggerID(o.client_order_id)
                const isHighlighted = trigger !== null && trigger === hoveredTrigger
                return (
                  <tr
                    key={i}
                    style={{
                      borderBottom: '1px solid var(--border)',
                      background: isHighlighted ? 'rgba(61,122,255,0.12)' : 'transparent',
                      transition: 'background 0.1s',
                    }}
                    onMouseEnter={e => handleRowEnter(trigger, e)}
                    onMouseMove={handleRowMove}
                    onMouseLeave={() => setHoveredTrigger(null)}
                  >
                    <td style={{ ...tdCss, color: 'var(--text-secondary)', fontSize: 11 }}>
                      {new Date(o.timestamp).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })}
                    </td>
                    <td style={{ ...tdCss, fontWeight: 600, color: o.side.toLowerCase() === 'buy' ? 'var(--accent-green)' : 'var(--accent-red)' }}>{o.side}</td>
                    <td style={tdCss}>{o.quantity}</td>
                    <td style={tdCss}>{o.price > 0 ? o.price.toFixed(4) : '市价'}</td>
                    <td style={{ ...tdCss, fontSize: 10, color: o.status === 'FILLED' || o.status === 'filled' ? 'var(--accent-green)' : 'var(--text-dim)' }}>{o.status}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
    </Panel>
  )

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
      {renderTable(ordersA, labelA, colorA)}
      {renderTable(ordersB, labelB, colorB)}
      {spreadInfo && (
        <div style={{
          position: 'fixed', left: tooltipPos.x + 12, top: tooltipPos.y - 40,
          zIndex: 9999, pointerEvents: 'none',
          background: 'var(--bg-panel)', border: '1px solid rgba(61,122,255,0.4)',
          borderRadius: 8, padding: '6px 14px',
          boxShadow: '0 4px 20px rgba(0,0,0,0.4)',
          display: 'flex', alignItems: 'center', gap: 8,
          fontSize: 12, fontFamily: 'var(--font-mono)', whiteSpace: 'nowrap',
        }}>
          <span style={{ color: colorA }}>{labelA} {spreadInfo.priceA.toFixed(4)}</span>
          <span style={{ color: 'var(--text-dim)' }}>−</span>
          <span style={{ color: colorB }}>{labelB} {spreadInfo.priceB.toFixed(4)}</span>
          <span style={{ color: 'var(--text-dim)' }}>=</span>
          <span style={{
            fontWeight: 700,
            color: spreadInfo.spread >= 0 ? 'var(--accent-green)' : 'var(--accent-red)',
          }}>
            {spreadInfo.spread >= 0 ? '+' : ''}{spreadInfo.spread.toFixed(4)}
          </span>
          <span style={{
            fontSize: 11, fontWeight: 600,
            color: spreadInfo.pct >= 0 ? 'var(--accent-green)' : 'var(--accent-red)',
            opacity: 0.8,
          }}>
            ({spreadInfo.pct >= 0 ? '+' : ''}{spreadInfo.pct.toFixed(4)}%)
          </span>
        </div>
      )}
    </div>
  )
}
