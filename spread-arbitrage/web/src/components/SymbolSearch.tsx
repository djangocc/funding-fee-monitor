import { useState, useRef, useEffect } from 'react'

// Common perpetual futures symbols across exchanges
const COMMON_SYMBOLS = [
  'BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT',
  'DOGEUSDT', 'ADAUSDT', 'AVAXUSDT', 'DOTUSDT', 'LINKUSDT',
  'MATICUSDT', 'UNIUSDT', 'ATOMUSDT', 'LTCUSDT', 'ETCUSDT',
  'FILUSDT', 'APTUSDT', 'NEARUSDT', 'ARBUSDT', 'OPUSDT',
  'SUIUSDT', 'SEIUSDT', 'TIAUSDT', 'JUPUSDT', 'WLDUSDT',
  'MKRUSDT', 'AAVEUSDT', 'INJUSDT', 'RAVEUSDT', 'PEPEUSDT',
  'WIFUSDT', 'BONKUSDT', 'FLOKIUSDT', 'SHIBUSDT', 'FETUSDT',
  'RUNEUSDT', 'STXUSDT', 'IMXUSDT', 'GRTUSDT', 'SNXUSDT',
]

interface SymbolSearchProps {
  value: string
  onChange: (symbol: string) => void
  placeholder?: string
}

export function SymbolSearch({ value, onChange, placeholder = 'Search symbol...' }: SymbolSearchProps) {
  const [query, setQuery] = useState(value)
  const [isOpen, setIsOpen] = useState(false)
  const [highlightIndex, setHighlightIndex] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  const filtered = query
    ? COMMON_SYMBOLS.filter(s => s.toLowerCase().includes(query.toLowerCase()))
    : COMMON_SYMBOLS

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  useEffect(() => {
    setQuery(value)
  }, [value])

  const selectSymbol = (symbol: string) => {
    setQuery(symbol)
    onChange(symbol)
    setIsOpen(false)
    setHighlightIndex(-1)
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setHighlightIndex(prev => Math.min(prev + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setHighlightIndex(prev => Math.max(prev - 1, 0))
    } else if (e.key === 'Enter' && highlightIndex >= 0) {
      e.preventDefault()
      selectSymbol(filtered[highlightIndex])
    } else if (e.key === 'Escape') {
      setIsOpen(false)
    }
  }

  useEffect(() => {
    if (highlightIndex >= 0 && listRef.current) {
      const el = listRef.current.children[highlightIndex] as HTMLElement
      el?.scrollIntoView({ block: 'nearest' })
    }
  }, [highlightIndex])

  const highlightMatch = (text: string) => {
    if (!query) return text
    const idx = text.toLowerCase().indexOf(query.toLowerCase())
    if (idx === -1) return text
    return (
      <>
        {text.slice(0, idx)}
        <span style={{ color: 'var(--accent-blue)', fontWeight: 600 }}>{text.slice(idx, idx + query.length)}</span>
        {text.slice(idx + query.length)}
      </>
    )
  }

  return (
    <div ref={containerRef} style={{ position: 'relative', width: '100%' }}>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        background: 'var(--bg-input)',
        border: `1px solid ${isOpen ? 'var(--border-focus)' : 'var(--border)'}`,
        borderRadius: 10,
        padding: '0 14px',
        transition: 'border-color 0.2s, box-shadow 0.2s',
        boxShadow: isOpen ? '0 0 0 3px rgba(61, 122, 255, 0.1)' : 'none',
      }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--text-dim)" strokeWidth="2.5" strokeLinecap="round" style={{ flexShrink: 0 }}>
          <circle cx="11" cy="11" r="8" />
          <line x1="21" y1="21" x2="16.65" y2="16.65" />
        </svg>
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value.toUpperCase())
            setIsOpen(true)
            setHighlightIndex(-1)
          }}
          onFocus={() => setIsOpen(true)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          style={{
            flex: 1,
            background: 'none',
            border: 'none',
            outline: 'none',
            color: 'var(--text-primary)',
            fontFamily: 'var(--font-mono)',
            fontSize: 14,
            fontWeight: 500,
            padding: '12px 10px',
            letterSpacing: '0.02em',
          }}
        />
        {query && (
          <button
            onClick={() => { setQuery(''); onChange(''); inputRef.current?.focus() }}
            style={{
              background: 'none', border: 'none', color: 'var(--text-dim)',
              cursor: 'pointer', padding: 4, display: 'flex', fontSize: 16,
            }}
          >
            &times;
          </button>
        )}
      </div>

      {isOpen && filtered.length > 0 && (
        <div
          ref={listRef}
          style={{
            position: 'absolute',
            top: 'calc(100% + 4px)',
            left: 0,
            right: 0,
            background: 'var(--bg-card)',
            border: '1px solid var(--border)',
            borderRadius: 10,
            maxHeight: 280,
            overflowY: 'auto',
            zIndex: 100,
            boxShadow: '0 12px 40px rgba(0,0,0,0.5)',
            animation: 'fadeIn 0.15s ease-out',
          }}
        >
          {filtered.map((symbol, idx) => (
            <div
              key={symbol}
              onClick={() => selectSymbol(symbol)}
              style={{
                padding: '10px 16px',
                cursor: 'pointer',
                fontFamily: 'var(--font-mono)',
                fontSize: 13,
                fontWeight: 500,
                letterSpacing: '0.03em',
                color: symbol === value ? 'var(--accent-blue)' : 'var(--text-primary)',
                background: idx === highlightIndex ? 'var(--bg-hover)' : 'transparent',
                transition: 'background 0.1s',
                borderBottom: idx < filtered.length - 1 ? '1px solid var(--border)' : 'none',
              }}
              onMouseEnter={() => setHighlightIndex(idx)}
            >
              {highlightMatch(symbol)}
              <span style={{ float: 'right', color: 'var(--text-dim)', fontSize: 11, fontWeight: 400 }}>
                {symbol.replace('USDT', '')}/USDT
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
