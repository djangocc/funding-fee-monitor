const EXCHANGES = [
  { id: 'binance', name: 'Binance', color: '#f0b90b' },
  { id: 'aster', name: 'Aster', color: '#8b5cf6' },
  { id: 'okx', name: 'OKX', color: '#00c8ff' },
] as const

interface ExchangePickerProps {
  label: string
  value: string
  disabledValue?: string
  onChange: (exchange: string) => void
}

export function ExchangePicker({ label, value, disabledValue, onChange }: ExchangePickerProps) {
  return (
    <div>
      <div style={{
        fontSize: 11,
        fontWeight: 600,
        color: 'var(--text-dim)',
        textTransform: 'uppercase',
        letterSpacing: '0.1em',
        marginBottom: 8,
      }}>
        {label}
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        {EXCHANGES.map(ex => {
          const isSelected = value === ex.id
          const isDisabled = disabledValue === ex.id
          return (
            <button
              key={ex.id}
              onClick={() => !isDisabled && onChange(ex.id)}
              style={{
                flex: 1,
                padding: '10px 0',
                border: `1.5px solid ${isSelected ? ex.color : isDisabled ? 'var(--border)' : 'var(--border)'}`,
                borderRadius: 8,
                background: isSelected ? `${ex.color}15` : 'var(--bg-input)',
                color: isDisabled ? 'var(--text-dim)' : isSelected ? ex.color : 'var(--text-secondary)',
                cursor: isDisabled ? 'not-allowed' : 'pointer',
                fontFamily: 'var(--font-display)',
                fontSize: 13,
                fontWeight: isSelected ? 600 : 500,
                transition: 'all 0.15s',
                opacity: isDisabled ? 0.4 : 1,
                position: 'relative',
              }}
            >
              {isSelected && (
                <span style={{
                  position: 'absolute',
                  top: -1,
                  right: -1,
                  width: 8,
                  height: 8,
                  borderRadius: '50%',
                  background: ex.color,
                  boxShadow: `0 0 8px ${ex.color}80`,
                }} />
              )}
              {ex.name}
            </button>
          )
        })}
      </div>
    </div>
  )
}
