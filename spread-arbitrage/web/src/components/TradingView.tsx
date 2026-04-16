import { useState, useEffect, useCallback, useRef } from 'react'
import { useWebSocket, type WSEvent } from '../hooks/useWebSocket'
import * as api from '../api'

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

interface TradingViewProps {
  symbol: string
  exchangeA: string
  exchangeB: string
}

export function TradingView({ symbol, exchangeA, exchangeB }: TradingViewProps) {
  const [quoteA, setQuoteA] = useState<Quote | null>(null)
  const [quoteB, setQuoteB] = useState<Quote | null>(null)
  const [tasks, setTasks] = useState<api.Task[]>([])
  const [spreads, setSpreads] = useState<Map<string, SpreadUpdate>>(new Map())
  const [positions, setPositions] = useState<api.Position[]>([])
  const [trades, setTrades] = useState<api.Trade[]>([])
  const [showCreatePanel, setShowCreatePanel] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const subscribed = useRef(false)

  // Task form state
  const [direction, setDirection] = useState<'long_spread' | 'short_spread'>('short_spread')
  const [openThreshold, setOpenThreshold] = useState('0.5')
  const [closeThreshold, setCloseThreshold] = useState('0.1')
  const [confirmCount, setConfirmCount] = useState('3')
  const [qtyPerOrder, setQtyPerOrder] = useState('100')
  const [maxPositionQty, setMaxPositionQty] = useState('500')
  const [latencyMs, setLatencyMs] = useState('1000')

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
    try {
      const [trA, trB] = await Promise.all([
        api.getTradesByExchange(exchangeA, symbol),
        api.getTradesByExchange(exchangeB, symbol),
      ])
      const merged = [...trA, ...trB].sort((a, b) =>
        new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()
      )
      setTrades(merged.slice(0, 50))
    } catch { /* ignore */ }
  }, [exchangeA, exchangeB, symbol])

  const handleWS = useCallback((event: WSEvent) => {
    if (event.type === 'quote') {
      const d = event.data
      if (d.symbol === symbol || d.symbol === '') {
        if (d.exchange === exchangeA) setQuoteA({ bid: d.bid, ask: d.ask, timestamp: d.timestamp })
        if (d.exchange === exchangeB) setQuoteB({ bid: d.bid, ask: d.ask, timestamp: d.timestamp })
      }
    }
    if (event.type === 'spread_update') {
      setSpreads(prev => new Map(prev).set(event.task_id, event.data))
    }
    if (event.type === 'task_status' || event.type === 'trade_executed') {
      refreshTasks()
      refreshPositions()
    }
    if (event.type === 'error') {
      setError(JSON.stringify(event.data))
      refreshTasks()
    }
  }, [symbol, exchangeA, exchangeB, refreshTasks, refreshPositions])

  const { connected } = useWebSocket(handleWS)

  useEffect(() => { refreshTasks() }, [refreshTasks])
  useEffect(() => { refreshPositions() }, [refreshPositions])
  useEffect(() => { refreshTrades() }, [refreshTrades])

  // Auto-refresh positions every 15s
  useEffect(() => {
    const iv = setInterval(() => { refreshPositions(); refreshTrades() }, 15000)
    return () => clearInterval(iv)
  }, [refreshPositions, refreshTrades])

  const spread = (quoteA && quoteB) ? (quoteA.bid - quoteB.ask) : null

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
            <span style={{ color: 'var(--text-dim)', fontSize: 12 }}>vs</span>
            <span style={{ fontSize: 12, fontWeight: 600, color: colorB, background: `${colorB}15`, padding: '3px 10px', borderRadius: 6 }}>
              {exchangeB.toUpperCase()}
            </span>
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{
            width: 7, height: 7, borderRadius: '50%',
            background: connected ? 'var(--accent-green)' : 'var(--accent-red)',
            boxShadow: connected ? '0 0 8px var(--accent-green)' : 'none',
            display: 'inline-block',
          }} />
          <span style={{ fontSize: 12, color: 'var(--text-dim)', fontFamily: 'var(--font-mono)' }}>
            {connected ? 'LIVE' : 'OFFLINE'}
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

        {/* Price Cards */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12, marginBottom: 24, animation: 'fadeIn 0.3s ease-out' }}>
          <PriceCard label={exchangeA.toUpperCase()} quote={quoteA} color={colorA} />
          <PriceCard label={exchangeB.toUpperCase()} quote={quoteB} color={colorB} />
          <div style={{
            background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 12, padding: '14px 18px',
          }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>
              Spread (A.bid - B.ask)
            </div>
            <div style={{
              fontSize: 22, fontWeight: 700, fontFamily: 'var(--font-mono)',
              color: spread !== null ? (spread >= 0 ? 'var(--accent-green)' : 'var(--accent-red)') : 'var(--text-dim)',
            }}>
              {spread !== null ? spread.toFixed(6) : '—'}
            </div>
          </div>
        </div>

        {/* Main grid */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 24 }}>
          {/* Tasks */}
          <Panel
            title="Tasks"
            action={
              <button onClick={() => setShowCreatePanel(!showCreatePanel)} style={{
                background: showCreatePanel ? 'var(--bg-hover)' : 'var(--accent-blue)',
                color: showCreatePanel ? 'var(--text-secondary)' : '#fff',
                border: 'none', borderRadius: 6, padding: '5px 12px',
                fontSize: 12, fontWeight: 600, cursor: 'pointer',
              }}>
                {showCreatePanel ? 'Cancel' : '+ New'}
              </button>
            }
          >
            {showCreatePanel && (
              <div style={{ padding: 18, borderBottom: '1px solid var(--border)', background: 'var(--bg-card)', animation: 'fadeIn 0.2s ease-out' }}>
                {/* Direction */}
                <div style={{ marginBottom: 14 }}>
                  <label style={labelCss}>Direction</label>
                  <div style={hintCss}>
                    {direction === 'short_spread'
                      ? `做空价差：当 ${exchangeA.toUpperCase()} - ${exchangeB.toUpperCase()} 价差大于开仓阈值时开仓（卖A买B），价差缩小到平仓阈值时平仓`
                      : `做多价差：当 ${exchangeA.toUpperCase()} - ${exchangeB.toUpperCase()} 价差小于开仓阈值时开仓（买A卖B），价差扩大到平仓阈值时平仓`
                    }
                  </div>
                  <select value={direction} onChange={e => setDirection(e.target.value as any)} style={{ ...inputCss, fontFamily: 'var(--font-display)' }}>
                    <option value="short_spread">做空价差 (Short Spread) — 预期价差缩小</option>
                    <option value="long_spread">做多价差 (Long Spread) — 预期价差扩大</option>
                  </select>
                </div>

                {/* Thresholds */}
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 14 }}>
                  <div>
                    <label style={labelCss}>开仓阈值 (Open Threshold)</label>
                    <div style={hintCss}>
                      {direction === 'short_spread'
                        ? '价差 > 此值时触发开仓信号（绝对价差，单位与标的价格相同）'
                        : '价差 < 此值时触发开仓信号（绝对价差，单位与标的价格相同）'
                      }
                    </div>
                    <input type="number" step="0.001" value={openThreshold} onChange={e => setOpenThreshold(e.target.value)} style={inputCss} />
                  </div>
                  <div>
                    <label style={labelCss}>平仓阈值 (Close Threshold)</label>
                    <div style={hintCss}>
                      {direction === 'short_spread'
                        ? '价差 < 此值时触发平仓信号（应小于开仓阈值）'
                        : '价差 > 此值时触发平仓信号（应大于开仓阈值）'
                      }
                    </div>
                    <input type="number" step="0.001" value={closeThreshold} onChange={e => setCloseThreshold(e.target.value)} style={inputCss} />
                  </div>
                </div>

                {/* Confirm count */}
                <div style={{ marginBottom: 14 }}>
                  <label style={labelCss}>连续确认次数 (Confirm Count)</label>
                  <div style={hintCss}>价差需连续满足条件多少次才真正触发开仓/平仓，避免瞬间波动导致误操作</div>
                  <input type="number" min="1" value={confirmCount} onChange={e => setConfirmCount(e.target.value)} style={inputCss} />
                </div>

                {/* Quantity */}
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 14 }}>
                  <div>
                    <label style={labelCss}>每次下单数量 (Qty per Order)</label>
                    <div style={hintCss}>每次开仓或平仓的标的数量（不是 USDT 金额）</div>
                    <input type="number" step="0.1" value={qtyPerOrder} onChange={e => setQtyPerOrder(e.target.value)} style={inputCss} />
                  </div>
                  <div>
                    <label style={labelCss}>最大持仓量 (Max Position)</label>
                    <div style={hintCss}>单边最大持仓数量，达到上限后不再开仓</div>
                    <input type="number" step="0.1" value={maxPositionQty} onChange={e => setMaxPositionQty(e.target.value)} style={inputCss} />
                  </div>
                </div>

                {/* Latency */}
                <div style={{ marginBottom: 16 }}>
                  <label style={labelCss}>最大数据延迟 (Max Latency)</label>
                  <div style={hintCss}>行情数据超过此延迟（毫秒）时不做交易决策，防止用过时数据下单</div>
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
                <TaskRow key={task.id} task={task} spreadData={spreads.get(task.id)} onRefresh={() => { refreshTasks(); refreshPositions() }} />
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

        {/* Trades */}
        <Panel title="Recent Trades">
          <div style={{ maxHeight: 300, overflowY: 'auto' }}>
            {trades.length === 0 ? (
              <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>No trades yet</div>
            ) : (
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--border)' }}>
                    {['Time', 'Exchange', 'Side', 'Qty', 'Price', 'Fee'].map(h => (
                      <th key={h} style={{ padding: '10px 14px', textAlign: 'left', fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.06em' }}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {trades.map((t, i) => (
                    <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                      <td style={{ padding: '10px 14px', fontSize: 12, fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)' }}>{new Date(t.timestamp).toLocaleTimeString()}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, color: EXCHANGE_COLORS[t.exchange] || 'var(--text-primary)' }}>{t.exchange.toUpperCase()}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 600, fontFamily: 'var(--font-mono)', color: t.side.toLowerCase() === 'buy' ? 'var(--accent-green)' : 'var(--accent-red)' }}>{t.side}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)' }}>{t.quantity}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)' }}>{t.price.toFixed(4)}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)', color: 'var(--text-dim)' }}>{t.fee.toFixed(6)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </Panel>
      </div>
    </div>
  )
}

/* ── Sub-components ── */

function PriceCard({ label, quote, color }: { label: string; quote: Quote | null; color: string }) {
  return (
    <div style={{ background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 12, padding: '14px 18px' }}>
      <div style={{ fontSize: 11, fontWeight: 600, color, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 8 }}>{label}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text-dim)', marginBottom: 2 }}>BID</div>
          <div style={{ fontSize: 16, fontWeight: 700, fontFamily: 'var(--font-mono)', color: 'var(--accent-green)' }}>
            {quote ? quote.bid.toFixed(4) : '—'}
          </div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text-dim)', marginBottom: 2 }}>ASK</div>
          <div style={{ fontSize: 16, fontWeight: 700, fontFamily: 'var(--font-mono)', color: 'var(--accent-red)' }}>
            {quote ? quote.ask.toFixed(4) : '—'}
          </div>
        </div>
      </div>
    </div>
  )
}

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

function TaskRow({ task, spreadData, onRefresh }: { task: api.Task; spreadData?: SpreadUpdate; onRefresh: () => void }) {
  const isRunning = task.status === 'running'
  const btn = (label: string, color: string, onClick: () => void) => (
    <button onClick={onClick} style={{
      padding: '4px 10px', border: 'none', borderRadius: 5,
      background: `${color}18`, color, fontSize: 11, fontWeight: 600, cursor: 'pointer',
    }}>{label}</button>
  )

  return (
    <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-mono)', color: task.direction === 'short_spread' ? 'var(--accent-red)' : 'var(--accent-green)' }}>
            {task.direction === 'short_spread' ? 'SHORT' : 'LONG'}
          </span>
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: isRunning ? 'var(--accent-green)' : 'var(--text-dim)', display: 'inline-block', animation: isRunning ? 'pulse 2s infinite' : 'none' }} />
          <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>{isRunning ? 'RUNNING' : 'STOPPED'}</span>
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
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 14px', fontSize: 11, color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)' }}>
        <span title="价差超过此阈值触发开仓">开仓: {task.open_threshold}</span>
        <span title="价差达到此阈值触发平仓">平仓: {task.close_threshold}</span>
        <span title="连续满足条件的次数">确认: {task.confirm_count}次</span>
        <span title="每次下单的标的数量">单笔: {task.quantity_per_order}</span>
        <span title="最大持仓数量上限">上限: {task.max_position_qty}</span>
      </div>
      {spreadData && (
        <div style={{ display: 'flex', gap: 14, fontSize: 11, marginTop: 6, fontFamily: 'var(--font-mono)' }}>
          <span style={{ color: 'var(--accent-blue)' }} title="当前连续满足开仓条件的次数 / 所需次数">
            开仓确认 {spreadData.open_counter}/{task.confirm_count}
          </span>
          <span style={{ color: 'var(--accent-amber)' }} title="当前连续满足平仓条件的次数 / 所需次数">
            平仓确认 {spreadData.close_counter}/{task.confirm_count}
          </span>
          <span style={{ color: 'var(--text-dim)' }} title="当前已开仓的数量">
            持仓: {task.position_qty ?? 0}
          </span>
        </div>
      )}
    </div>
  )
}

interface Quote {
  bid: number
  ask: number
  timestamp: string
}
