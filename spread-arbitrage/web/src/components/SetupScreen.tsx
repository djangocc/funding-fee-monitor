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
  const [testResults, setTestResults] = useState<Record<string, { loading: boolean; result?: api.TestTradeResult; error?: string }>>({})

  const canEnter = symbol.length > 0 && exchangeA !== exchangeB

  const handleTestTrade = async (exchange: string) => {
    setTestResults(prev => ({ ...prev, [exchange]: { loading: true } }))
    try {
      const result = await api.testTrade(exchange)
      setTestResults(prev => ({ ...prev, [exchange]: { loading: false, result } }))
    } catch (e: any) {
      setTestResults(prev => ({ ...prev, [exchange]: { loading: false, error: e.message } }))
    }
  }

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
          <div style={{ display: 'grid', gridTemplateColumns: '1fr auto 1fr', gap: 10, alignItems: 'end', marginBottom: 28 }}>
            <ExchangePicker label="Exchange A" value={exchangeA} disabledValue={exchangeB} onChange={setExchangeA} />
            <button
              onClick={() => { setExchangeA(exchangeB); setExchangeB(exchangeA) }}
              style={{
                background: 'none', border: '1px solid var(--border)', borderRadius: 8,
                color: 'var(--text-dim)', cursor: 'pointer', padding: '8px 10px',
                fontSize: 16, lineHeight: 1, marginBottom: 1,
                transition: 'all 0.15s',
              }}
              onMouseEnter={e => { e.currentTarget.style.borderColor = 'var(--accent-blue)'; e.currentTarget.style.color = 'var(--accent-blue)' }}
              onMouseLeave={e => { e.currentTarget.style.borderColor = 'var(--border)'; e.currentTarget.style.color = 'var(--text-dim)' }}
              title="交换 A / B"
            >⇄</button>
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

        {/* Test Trading Section */}
        <div style={{
          background: 'var(--bg-panel)', border: '1px solid var(--border)', borderRadius: 14,
          padding: 20, marginBottom: 32,
        }}>
          <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 4 }}>交易连通性测试</div>
          <div style={{ fontSize: 12, color: 'var(--text-dim)', marginBottom: 16 }}>
            使用 DOGEUSDT 进行一次最小金额的买入+卖出（约 5 USDT），验证 API Key 和交易权限是否正常
          </div>
          <div style={{ display: 'flex', gap: 10 }}>
            {['binance', 'aster', 'okx'].map(ex => {
              const state = testResults[ex]
              const color = EXCHANGE_COLORS[ex]
              return (
                <div key={ex} style={{ flex: 1 }}>
                  <button
                    onClick={() => handleTestTrade(ex)}
                    disabled={state?.loading}
                    style={{
                      width: '100%', padding: '10px 0', border: `1px solid ${color}40`,
                      borderRadius: 8, background: state?.loading ? 'var(--bg-hover)' : `${color}10`,
                      color: state?.loading ? 'var(--text-dim)' : color,
                      fontFamily: 'var(--font-display)', fontSize: 13, fontWeight: 600,
                      cursor: state?.loading ? 'wait' : 'pointer',
                    }}
                  >
                    {state?.loading ? '测试中...' : `测试 ${ex.toUpperCase()}`}
                  </button>
                  {state && !state.loading && (
                    <div style={{ marginTop: 8, fontSize: 11, lineHeight: 1.6 }}>
                      {state.result?.success ? (
                        <div style={{ color: 'var(--accent-green)' }}>
                          通过 — 买卖均正常
                        </div>
                      ) : state.result?.error ? (
                        <div style={{ color: 'var(--accent-red)' }}>
                          失败: {state.result.error}
                        </div>
                      ) : state.error ? (
                        <div style={{ color: 'var(--accent-red)' }}>
                          请求失败: {state.error}
                        </div>
                      ) : null}
                      {state.result?.steps?.map((step, i) => (
                        <div key={i} style={{ color: 'var(--text-dim)', fontFamily: 'var(--font-mono)', fontSize: 10 }}>
                          {step}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
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
                      pos: {t.current_position ?? 0}
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
