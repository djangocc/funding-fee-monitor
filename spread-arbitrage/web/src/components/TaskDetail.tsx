import { useState, useEffect } from 'react'
import type { Task, Position, Trade } from '../api'
import { getPositions, getTrades } from '../api'

interface SpreadUpdate {
  spread: number
  open_counter: number
  close_counter: number
  bid_a: number
  ask_a: number
  bid_b: number
  ask_b: number
}

interface TaskDetailProps {
  task: Task
  spreadData?: SpreadUpdate
  onStart: () => void
  onStop: () => void
  onManualOpen: () => void
  onManualClose: () => void
  onEdit: () => void
  onDelete: () => void
}

export function TaskDetail({
  task,
  spreadData,
  onStart,
  onStop,
  onManualOpen,
  onManualClose,
  onEdit,
  onDelete,
}: TaskDetailProps) {
  const [positions, setPositions] = useState<Position[]>([])
  const [trades, setTrades] = useState<Trade[]>([])

  useEffect(() => {
    getPositions(task.id).then(setPositions).catch(console.error)
    getTrades(task.id).then(setTrades).catch(console.error)
  }, [task.id])

  const cardStyle = {
    background: '#2a2a2a',
    border: '1px solid #444',
    borderRadius: '8px',
    padding: '20px',
    marginBottom: '16px',
  }

  const badgeStyle = (color: string) => ({
    display: 'inline-block',
    padding: '4px 12px',
    borderRadius: '12px',
    fontSize: '12px',
    fontWeight: 'bold' as const,
    background: color,
    color: '#fff',
    marginLeft: '8px',
  })

  const buttonStyle = {
    padding: '6px 12px',
    border: 'none',
    borderRadius: '4px',
    cursor: 'pointer',
    fontSize: '12px',
    fontWeight: 'bold' as const,
    marginRight: '8px',
  }

  const tableStyle = {
    width: '100%',
    borderCollapse: 'collapse' as const,
    marginTop: '8px',
  }

  return (
    <div style={cardStyle}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
        <div>
          <span style={{ fontSize: '18px', fontWeight: 'bold' }}>{task.symbol}</span>
          <span style={{ marginLeft: '12px', color: '#aaa' }}>
            {task.exchange_a} vs {task.exchange_b}
          </span>
          <span style={badgeStyle(task.direction === 'long_spread' ? '#2196f3' : '#ff9800')}>
            {task.direction === 'long_spread' ? 'LONG' : 'SHORT'}
          </span>
          <span style={badgeStyle(task.status === 'running' ? '#4caf50' : '#666')}>
            {task.status.toUpperCase()}
          </span>
        </div>
      </div>

      {/* Config */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px', marginBottom: '16px', fontSize: '14px' }}>
        <div>
          <span style={{ color: '#aaa' }}>Open/Close:</span> {task.open_threshold.toFixed(4)} / {task.close_threshold.toFixed(4)}
        </div>
        <div>
          <span style={{ color: '#aaa' }}>Confirm:</span> {task.confirm_count}
        </div>
        <div>
          <span style={{ color: '#aaa' }}>Qty/Order:</span> {task.quantity_per_order}
        </div>
        <div>
          <span style={{ color: '#aaa' }}>Max Position:</span> {task.max_position_qty}
        </div>
        <div>
          <span style={{ color: '#aaa' }}>Latency:</span> {task.data_max_latency_ms}ms
        </div>
      </div>

      {/* Live Data */}
      {spreadData && (
        <div style={{ background: '#1e1e1e', padding: '12px', borderRadius: '4px', marginBottom: '16px' }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: '12px', fontSize: '14px' }}>
            <div>
              <span style={{ color: '#aaa' }}>Spread:</span> <span style={{ color: '#4caf50', fontWeight: 'bold' }}>{spreadData.spread.toFixed(4)}</span>
            </div>
            <div>
              <span style={{ color: '#aaa' }}>Open Counter:</span> {spreadData.open_counter}/{task.confirm_count}
            </div>
            <div>
              <span style={{ color: '#aaa' }}>Close Counter:</span> {spreadData.close_counter}/{task.confirm_count}
            </div>
            <div>
              <span style={{ color: '#aaa' }}>Position:</span> {task.position_qty ?? 0}
            </div>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px', fontSize: '12px', marginTop: '8px' }}>
            <div>
              <span style={{ color: '#888' }}>Bid A:</span> {spreadData.bid_a.toFixed(2)}
            </div>
            <div>
              <span style={{ color: '#888' }}>Ask A:</span> {spreadData.ask_a.toFixed(2)}
            </div>
            <div>
              <span style={{ color: '#888' }}>Bid B:</span> {spreadData.bid_b.toFixed(2)}
            </div>
            <div>
              <span style={{ color: '#888' }}>Ask B:</span> {spreadData.ask_b.toFixed(2)}
            </div>
          </div>
        </div>
      )}

      {/* Actions */}
      <div style={{ marginBottom: '16px' }}>
        {task.status === 'stopped' && (
          <button onClick={onStart} style={{ ...buttonStyle, background: '#4caf50', color: '#fff' }}>
            Start
          </button>
        )}
        {task.status === 'running' && (
          <button onClick={onStop} style={{ ...buttonStyle, background: '#f44336', color: '#fff' }}>
            Stop
          </button>
        )}
        <button onClick={onManualOpen} style={{ ...buttonStyle, background: '#2196f3', color: '#fff' }}>
          Manual Open
        </button>
        <button onClick={onManualClose} style={{ ...buttonStyle, background: '#ff9800', color: '#fff' }}>
          Manual Close
        </button>
        <button onClick={onEdit} style={{ ...buttonStyle, background: '#666', color: '#fff' }}>
          Edit
        </button>
        <button onClick={onDelete} style={{ ...buttonStyle, background: '#d32f2f', color: '#fff' }}>
          Delete
        </button>
      </div>

      {/* Positions */}
      <div style={{ marginBottom: '16px' }}>
        <h4 style={{ marginBottom: '8px' }}>Positions</h4>
        {positions.length === 0 ? (
          <div style={{ color: '#888', fontSize: '14px' }}>No positions</div>
        ) : (
          <table style={tableStyle}>
            <thead>
              <tr style={{ borderBottom: '1px solid #444' }}>
                <th style={{ textAlign: 'left', padding: '8px' }}>Exchange</th>
                <th style={{ textAlign: 'left', padding: '8px' }}>Side</th>
                <th style={{ textAlign: 'right', padding: '8px' }}>Size</th>
                <th style={{ textAlign: 'right', padding: '8px' }}>Entry Price</th>
                <th style={{ textAlign: 'right', padding: '8px' }}>PnL</th>
              </tr>
            </thead>
            <tbody>
              {positions.map((pos, idx) => (
                <tr key={idx} style={{ borderBottom: '1px solid #333' }}>
                  <td style={{ padding: '8px' }}>{pos.exchange}</td>
                  <td style={{ padding: '8px', color: pos.side === 'long' ? '#4caf50' : '#f44336' }}>{pos.side}</td>
                  <td style={{ padding: '8px', textAlign: 'right' }}>{pos.size}</td>
                  <td style={{ padding: '8px', textAlign: 'right' }}>{pos.entry_price.toFixed(2)}</td>
                  <td style={{ padding: '8px', textAlign: 'right', color: pos.unrealized_pnl >= 0 ? '#4caf50' : '#f44336' }}>
                    {pos.unrealized_pnl.toFixed(2)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Trades */}
      <div>
        <h4 style={{ marginBottom: '8px' }}>Trades</h4>
        {trades.length === 0 ? (
          <div style={{ color: '#888', fontSize: '14px' }}>No trades</div>
        ) : (
          <table style={tableStyle}>
            <thead>
              <tr style={{ borderBottom: '1px solid #444' }}>
                <th style={{ textAlign: 'left', padding: '8px' }}>Time</th>
                <th style={{ textAlign: 'left', padding: '8px' }}>Exchange</th>
                <th style={{ textAlign: 'left', padding: '8px' }}>Side</th>
                <th style={{ textAlign: 'right', padding: '8px' }}>Qty</th>
                <th style={{ textAlign: 'right', padding: '8px' }}>Price</th>
                <th style={{ textAlign: 'right', padding: '8px' }}>Fee</th>
              </tr>
            </thead>
            <tbody>
              {trades.map((trade, idx) => (
                <tr key={idx} style={{ borderBottom: '1px solid #333' }}>
                  <td style={{ padding: '8px', fontSize: '12px' }}>{new Date(trade.timestamp).toLocaleString()}</td>
                  <td style={{ padding: '8px' }}>{trade.exchange}</td>
                  <td style={{ padding: '8px', color: trade.side === 'buy' ? '#4caf50' : '#f44336' }}>{trade.side}</td>
                  <td style={{ padding: '8px', textAlign: 'right' }}>{trade.quantity}</td>
                  <td style={{ padding: '8px', textAlign: 'right' }}>{trade.price.toFixed(2)}</td>
                  <td style={{ padding: '8px', textAlign: 'right' }}>{trade.fee.toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
