import { useState, useEffect, useCallback } from 'react'
import * as api from '../api'

const EXCHANGE_COLORS: Record<string, string> = {
  binance: '#f0b90b',
  aster: '#8b5cf6',
  okx: '#00c8ff',
}

export function PositionsPage() {
  const [tasks, setTasks] = useState<api.Task[]>([])
  const [allPositions, setAllPositions] = useState<Map<string, api.Position[]>>(new Map())
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const taskList = await api.listTasks()
      setTasks(taskList)

      // Collect unique exchange+symbol pairs from tasks
      const pairs = new Set<string>()
      for (const t of taskList) {
        pairs.add(`${t.exchange_a}:${t.symbol}`)
        pairs.add(`${t.exchange_b}:${t.symbol}`)
      }

      const posMap = new Map<string, api.Position[]>()
      for (const pair of pairs) {
        const [ex, sym] = pair.split(':')
        try {
          const pos = await api.getPositionByExchange(ex, sym)
          if (pos.size > 0) {
            const key = sym
            const existing = posMap.get(key) || []
            existing.push(pos)
            posMap.set(key, existing)
          }
        } catch { /* ignore */ }
      }
      setAllPositions(posMap)
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // Auto-refresh every 15s
  useEffect(() => {
    const iv = setInterval(refresh, 15000)
    return () => clearInterval(iv)
  }, [refresh])

  const runningCount = tasks.filter(t => t.status === 'running').length

  return (
    <div style={{ minHeight: '100vh', background: 'var(--bg-deep)' }}>
      <header style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '12px 24px', borderBottom: '1px solid var(--border)', background: 'var(--bg-panel)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          <a href="/" style={{ color: 'var(--text-secondary)', textDecoration: 'none', fontSize: 18, padding: '4px 8px' }}>&larr;</a>
          <span style={{ fontFamily: 'var(--font-display)', fontSize: 18, fontWeight: 700 }}>All Positions</span>
        </div>
        <button onClick={refresh} style={{
          background: 'var(--bg-hover)', color: 'var(--text-secondary)',
          border: '1px solid var(--border)', borderRadius: 6, padding: '5px 14px',
          fontSize: 12, fontWeight: 600, cursor: 'pointer',
        }}>Refresh</button>
      </header>

      <div style={{ padding: 24, maxWidth: 1000, margin: '0 auto' }}>
        {/* Summary */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12, marginBottom: 24 }}>
          <SummaryCard label="Total Tasks" value={`${tasks.length}`} />
          <SummaryCard label="Running" value={`${runningCount}`} color={runningCount > 0 ? 'var(--accent-green)' : 'var(--text-dim)'} />
          <SummaryCard label="Symbols with Positions" value={`${allPositions.size}`} />
        </div>

        {/* Tasks overview */}
        <div style={{
          background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 14, overflow: 'hidden', marginBottom: 24,
        }}>
          <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)', fontSize: 14, fontWeight: 600 }}>
            Active Tasks
          </div>
          {tasks.length === 0 ? (
            <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>No tasks</div>
          ) : (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)' }}>
                  {['Symbol', 'Exchanges', 'Direction', 'Status', 'Thresholds', 'Position'].map(h => (
                    <th key={h} style={{ padding: '10px 14px', textAlign: 'left', fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.06em' }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {tasks.map(t => (
                  <tr key={t.id} style={{ borderBottom: '1px solid var(--border)', cursor: 'pointer' }}
                    onClick={() => window.location.href = `/trade/${t.symbol}/${t.exchange_a}/${t.exchange_b}`}
                  >
                    <td style={{ padding: '10px 14px', fontSize: 14, fontWeight: 600, fontFamily: 'var(--font-mono)' }}>{t.symbol}</td>
                    <td style={{ padding: '10px 14px', fontSize: 12 }}>
                      <span style={{ color: EXCHANGE_COLORS[t.exchange_a] }}>{t.exchange_a}</span>
                      <span style={{ color: 'var(--text-dim)', margin: '0 4px' }}>vs</span>
                      <span style={{ color: EXCHANGE_COLORS[t.exchange_b] }}>{t.exchange_b}</span>
                    </td>
                    <td style={{ padding: '10px 14px', fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-mono)', color: t.direction === 'short_spread' ? 'var(--accent-red)' : 'var(--accent-green)' }}>
                      {t.direction === 'short_spread' ? 'SHORT' : 'LONG'}
                    </td>
                    <td style={{ padding: '10px 14px' }}>
                      <span style={{
                        display: 'inline-flex', alignItems: 'center', gap: 4,
                        fontSize: 11, fontWeight: 600,
                        color: t.status === 'running' ? 'var(--accent-green)' : 'var(--text-dim)',
                      }}>
                        <span style={{ width: 6, height: 6, borderRadius: '50%', background: t.status === 'running' ? 'var(--accent-green)' : 'var(--text-dim)', display: 'inline-block' }} />
                        {t.status.toUpperCase()}
                      </span>
                    </td>
                    <td style={{ padding: '10px 14px', fontSize: 12, fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)' }}>
                      {t.open_threshold} / {t.close_threshold}
                    </td>
                    <td style={{ padding: '10px 14px', fontSize: 12, fontFamily: 'var(--font-mono)' }}>{t.current_position ?? 0}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {/* Positions by symbol */}
        <div style={{
          background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 14, overflow: 'hidden',
        }}>
          <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)', fontSize: 14, fontWeight: 600 }}>
            Exchange Positions
          </div>
          {loading ? (
            <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>Loading...</div>
          ) : allPositions.size === 0 ? (
            <div style={{ padding: 32, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>No open positions on any exchange</div>
          ) : (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--border)' }}>
                  {['Symbol', 'Exchange', 'Side', 'Size', 'Entry Price', 'PnL'].map(h => (
                    <th key={h} style={{ padding: '10px 14px', textAlign: 'left', fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.06em' }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {Array.from(allPositions.entries()).flatMap(([sym, posList]) =>
                  posList.map((pos, i) => (
                    <tr key={`${sym}-${i}`} style={{ borderBottom: '1px solid var(--border)' }}>
                      <td style={{ padding: '10px 14px', fontSize: 14, fontWeight: 600, fontFamily: 'var(--font-mono)' }}>{sym}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 500, color: EXCHANGE_COLORS[pos.exchange] || 'var(--text-primary)' }}>{pos.exchange.toUpperCase()}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 600, fontFamily: 'var(--font-mono)', color: pos.side === 'LONG' ? 'var(--accent-green)' : pos.side === 'SHORT' ? 'var(--accent-red)' : 'var(--text-dim)' }}>{pos.side || '—'}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)' }}>{pos.size}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontFamily: 'var(--font-mono)' }}>{pos.entry_price.toFixed(4)}</td>
                      <td style={{ padding: '10px 14px', fontSize: 13, fontWeight: 600, fontFamily: 'var(--font-mono)', color: pos.unrealized_pnl >= 0 ? 'var(--accent-green)' : 'var(--accent-red)' }}>
                        {pos.unrealized_pnl >= 0 ? '+' : ''}{pos.unrealized_pnl.toFixed(4)}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  )
}

function SummaryCard({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 12, padding: '14px 18px' }}>
      <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 700, fontFamily: 'var(--font-mono)', color: color || 'var(--text-primary)' }}>{value}</div>
    </div>
  )
}
