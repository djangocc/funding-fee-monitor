import { useState, type FormEvent } from 'react'
import type { Task, TaskCreateRequest } from '../api'

interface TaskFormProps {
  onSubmit: (req: TaskCreateRequest) => void
  initialValues?: Task
  onCancel?: () => void
}

export function TaskForm({ onSubmit, initialValues, onCancel }: TaskFormProps) {
  const [symbol, setSymbol] = useState(initialValues?.symbol || '')
  const [exchangeA, setExchangeA] = useState(initialValues?.exchange_a || 'binance')
  const [exchangeB, setExchangeB] = useState(initialValues?.exchange_b || 'aster')
  const [direction, setDirection] = useState<'long_spread' | 'short_spread'>(
    initialValues?.direction || 'long_spread'
  )
  const [openThreshold, setOpenThreshold] = useState(initialValues?.open_threshold?.toString() || '')
  const [closeThreshold, setCloseThreshold] = useState(initialValues?.close_threshold?.toString() || '')
  const [confirmCount, setConfirmCount] = useState(initialValues?.confirm_count?.toString() || '3')
  const [quantityPerOrder, setQuantityPerOrder] = useState(initialValues?.quantity_per_order?.toString() || '')
  const [maxPositionQty, setMaxPositionQty] = useState(initialValues?.max_position_qty?.toString() || '')
  const [dataMaxLatencyMs, setDataMaxLatencyMs] = useState(initialValues?.data_max_latency_ms?.toString() || '1000')
  const [errors, setErrors] = useState<string[]>([])

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    const validationErrors: string[] = []

    if (!symbol.trim()) validationErrors.push('Symbol is required')
    if (exchangeA === exchangeB) validationErrors.push('ExchangeA must be different from ExchangeB')

    const openThresholdNum = parseFloat(openThreshold)
    const closeThresholdNum = parseFloat(closeThreshold)
    const confirmCountNum = parseInt(confirmCount)
    const quantityPerOrderNum = parseFloat(quantityPerOrder)
    const maxPositionQtyNum = parseFloat(maxPositionQty)
    const dataMaxLatencyMsNum = parseInt(dataMaxLatencyMs)

    if (isNaN(openThresholdNum)) validationErrors.push('Open Threshold must be a valid number')
    if (isNaN(closeThresholdNum)) validationErrors.push('Close Threshold must be a valid number')
    if (isNaN(confirmCountNum) || confirmCountNum < 1) validationErrors.push('Confirm Count must be >= 1')
    if (isNaN(quantityPerOrderNum) || quantityPerOrderNum <= 0) validationErrors.push('Quantity Per Order must be > 0')
    if (isNaN(maxPositionQtyNum) || maxPositionQtyNum <= 0) validationErrors.push('Max Position Qty must be > 0')
    if (isNaN(dataMaxLatencyMsNum)) validationErrors.push('Data Max Latency Ms must be a valid number')

    if (direction === 'short_spread' && openThresholdNum <= closeThresholdNum) {
      validationErrors.push('For short_spread: Open Threshold must be > Close Threshold')
    }
    if (direction === 'long_spread' && openThresholdNum >= closeThresholdNum) {
      validationErrors.push('For long_spread: Open Threshold must be < Close Threshold')
    }

    if (validationErrors.length > 0) {
      setErrors(validationErrors)
      return
    }

    setErrors([])
    onSubmit({
      symbol: symbol.trim(),
      exchange_a: exchangeA,
      exchange_b: exchangeB,
      direction,
      open_threshold: openThresholdNum,
      close_threshold: closeThresholdNum,
      confirm_count: confirmCountNum,
      quantity_per_order: quantityPerOrderNum,
      max_position_qty: maxPositionQtyNum,
      data_max_latency_ms: dataMaxLatencyMsNum,
    })
  }

  const inputStyle = {
    padding: '8px',
    background: '#2a2a2a',
    border: '1px solid #444',
    color: '#e0e0e0',
    borderRadius: '4px',
    width: '100%',
  }

  const buttonStyle = {
    padding: '8px 16px',
    background: '#4caf50',
    color: '#fff',
    border: 'none',
    borderRadius: '4px',
    cursor: 'pointer',
    fontWeight: 'bold' as const,
  }

  const cancelButtonStyle = {
    ...buttonStyle,
    background: '#666',
    marginLeft: '8px',
  }

  return (
    <form onSubmit={handleSubmit} style={{ background: '#2a2a2a', padding: '20px', borderRadius: '8px', border: '1px solid #444' }}>
      <h3 style={{ marginBottom: '16px' }}>{initialValues ? 'Edit Task' : 'New Task'}</h3>

      {errors.length > 0 && (
        <div style={{ background: '#f44336', color: '#fff', padding: '12px', borderRadius: '4px', marginBottom: '16px' }}>
          {errors.map((err, idx) => (
            <div key={idx}>• {err}</div>
          ))}
        </div>
      )}

      <div style={{ display: 'grid', gap: '12px', gridTemplateColumns: '1fr 1fr' }}>
        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Symbol</label>
          <input type="text" value={symbol} onChange={(e) => setSymbol(e.target.value)} style={inputStyle} />
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Direction</label>
          <select value={direction} onChange={(e) => setDirection(e.target.value as 'long_spread' | 'short_spread')} style={inputStyle}>
            <option value="long_spread">Long Spread</option>
            <option value="short_spread">Short Spread</option>
          </select>
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Exchange A</label>
          <select value={exchangeA} onChange={(e) => setExchangeA(e.target.value)} style={inputStyle}>
            <option value="binance">Binance</option>
            <option value="aster">Aster</option>
            <option value="okx">OKX</option>
          </select>
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Exchange B</label>
          <select value={exchangeB} onChange={(e) => setExchangeB(e.target.value)} style={inputStyle}>
            <option value="binance">Binance</option>
            <option value="aster">Aster</option>
            <option value="okx">OKX</option>
          </select>
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Open Threshold</label>
          <input type="number" step="0.001" value={openThreshold} onChange={(e) => setOpenThreshold(e.target.value)} style={inputStyle} />
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Close Threshold</label>
          <input type="number" step="0.001" value={closeThreshold} onChange={(e) => setCloseThreshold(e.target.value)} style={inputStyle} />
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Confirm Count</label>
          <input type="number" min="1" value={confirmCount} onChange={(e) => setConfirmCount(e.target.value)} style={inputStyle} />
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Quantity Per Order</label>
          <input type="number" step="0.1" value={quantityPerOrder} onChange={(e) => setQuantityPerOrder(e.target.value)} style={inputStyle} />
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Max Position Qty</label>
          <input type="number" step="0.1" value={maxPositionQty} onChange={(e) => setMaxPositionQty(e.target.value)} style={inputStyle} />
        </div>

        <div>
          <label style={{ display: 'block', marginBottom: '4px', fontSize: '14px' }}>Data Max Latency (ms)</label>
          <input type="number" value={dataMaxLatencyMs} onChange={(e) => setDataMaxLatencyMs(e.target.value)} style={inputStyle} />
        </div>
      </div>

      <div style={{ marginTop: '16px', display: 'flex', justifyContent: 'flex-end' }}>
        <button type="submit" style={buttonStyle}>
          {initialValues ? 'Update' : 'Create'}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} style={cancelButtonStyle}>
            Cancel
          </button>
        )}
      </div>
    </form>
  )
}
