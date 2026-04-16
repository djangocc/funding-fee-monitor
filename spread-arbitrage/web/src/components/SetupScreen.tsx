import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { SymbolSearch } from './SymbolSearch'
import { ExchangePicker } from './ExchangePicker'
import * as api from '../api'

const EXCHANGE_COLORS: Record<string, string> = {
  binance: '#f0b90b',
  aster: '#8b5cf6',
  okx: '#00c8ff',
}

export function SetupScreen() {
  const navigate = useNavigate()
  const [symbol, setSymbol] = useState('')
  const [exchangeA, setExchangeA] = useState('binance')
  const [exchangeB, setExchangeB] = useState('aster')
  const [tasks, setTasks] = useState<api.Task[]>([])

  const canEnter = symbol.length > 0 && exchangeA !== exchangeB

  useEffect(() => {
    api.listTasks().then(setTasks).catch(() => {})
  }, [])

  const runningTasks = tasks.filter(t => t.status === 'running')

  return (
    <div style={{ minHeight: '100vh', padding: 20 }}>
      {/* Background */}
      <div style={{
        position: 'fixed', inset: 0,
        background: 'radial-gradient(ellipse at 30% 20%, rgba(61,122,255,0.06) 0%, transparent 60%), radial-gradient(ellipse at 70% 80%, rgba(168,85,247,0.04) 0%, transparent 60%)',
        pointerEvents: 'none',
      }} />

      <div style={{ position: 'relative', maxWidth: 800, margin: '0 auto', paddingTop: 40 }}>
        {/* Logo + nav */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 48 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <svg width="28" height="28" viewBox="0 0 28 28" fill="none">
              <rect x="2" y="8" width="10" height="16" rx="2" fill="var(--accent-blue)" opacity="0.8" />
              <rect x="16" y="4" width="10" height="20" rx="2" fill="var(--accent-purple)" opacity="0.8" />
              <path d="M12 16 L16 12" stroke="var(--text-primary)" strokeWidth="2" strokeLinecap="round" opacity="0.5" />
            </svg>
            <span style={{ fontFamily: 'var(--font-display)', fontSize: 22, fontWeight: 700, letterSpacing: '-0.02em' }}>
              Spread Arb
            </span>
          </div>
          <a href="/positions" style={{
            color: 'var(--text-secondary)', textDecoration: 'none',
            fontSize: 13, fontWeight: 500, padding: '6px 14px',
            border: '1px solid var(--border)', borderRadius: 8,
            transition: 'all 0.15s',
          }}>
            All Positions &rarr;
          </a>
        </div>

        {/* Selection card */}
        <div style={{
          background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 16, padding: 28,
          marginBottom: 32, animation: 'slideUp 0.5s ease-out',
        }}>
          <div style={{ marginBottom: 24 }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.1em', marginBottom: 8 }}>Symbol</div>
            <SymbolSearch value={symbol} onChange={setSymbol} placeholder="Search symbol..." />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 28 }}>
            <ExchangePicker label="Exchange A" value={exchangeA} disabledValue={exchangeB} onChange={setExchangeA} />
            <ExchangePicker label="Exchange B" value={exchangeB} disabledValue={exchangeA} onChange={setExchangeB} />
          </div>
          <button
            onClick={() => canEnter && navigate(`/trade/${symbol}/${exchangeA}/${exchangeB}`)}
            disabled={!canEnter}
            style={{
              width: '100%', padding: '14px 0', border: 'none', borderRadius: 10,
              background: canEnter ? 'linear-gradient(135deg, var(--accent-blue), var(--accent-purple))' : 'var(--bg-hover)',
              color: canEnter ? '#fff' : 'var(--text-dim)',
              fontFamily: 'var(--font-display)', fontSize: 15, fontWeight: 600,
              cursor: canEnter ? 'pointer' : 'not-allowed',
              boxShadow: canEnter ? '0 4px 20px rgba(61,122,255,0.3)' : 'none',
            }}
          >
            Enter Trading View
          </button>
        </div>

        {/* Active tasks overview */}
        {tasks.length > 0 && (
          <div style={{
            background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 14,
            overflow: 'hidden', animation: 'fadeIn 0.5s ease-out',
          }}>
            <div style={{
              display: 'flex', justifyContent: 'space-between', alignItems: 'center',
              padding: '14px 18px', borderBottom: '1px solid var(--border)',
            }}>
              <span style={{ fontSize: 14, fontWeight: 600 }}>
                Active Tasks
                {runningTasks.length > 0 && (
                  <span style={{ color: 'var(--accent-green)', marginLeft: 8, fontSize: 12 }}>
                    {runningTasks.length} running
                  </span>
                )}
              </span>
              <a href="/positions" style={{ color: 'var(--accent-blue)', textDecoration: 'none', fontSize: 12, fontWeight: 500 }}>
                View All &rarr;
              </a>
            </div>
            <div style={{ maxHeight: 300, overflowY: 'auto' }}>
              {tasks.map(t => (
                <div
                  key={t.id}
                  onClick={() => navigate(`/trade/${t.symbol}/${t.exchange_a}/${t.exchange_b}`)}
                  style={{
                    padding: '12px 18px', borderBottom: '1px solid var(--border)',
                    cursor: 'pointer', transition: 'background 0.15s',
                    display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                  }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'var(--bg-hover)')}
                  onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 14, fontWeight: 600 }}>{t.symbol}</span>
                    <span style={{ fontSize: 11, color: EXCHANGE_COLORS[t.exchange_a] }}>{t.exchange_a}</span>
                    <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>vs</span>
                    <span style={{ fontSize: 11, color: EXCHANGE_COLORS[t.exchange_b] }}>{t.exchange_b}</span>
                    <span style={{
                      fontSize: 11, fontWeight: 700, fontFamily: 'var(--font-mono)',
                      color: t.direction === 'short_spread' ? 'var(--accent-red)' : 'var(--accent-green)',
                    }}>
                      {t.direction === 'short_spread' ? 'SHORT' : 'LONG'}
                    </span>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{
                      width: 6, height: 6, borderRadius: '50%',
                      background: t.status === 'running' ? 'var(--accent-green)' : 'var(--text-dim)',
                      display: 'inline-block',
                      animation: t.status === 'running' ? 'pulse 2s infinite' : 'none',
                    }} />
                    <span style={{ fontSize: 11, color: 'var(--text-dim)', fontFamily: 'var(--font-mono)' }}>
                      pos: {t.position_qty ?? 0}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
