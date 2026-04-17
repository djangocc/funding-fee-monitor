import { useMemo } from 'react'

interface OrderBookLevel {
  price: number
  quantity: number
}

interface OrderBookData {
  exchange: string
  symbol: string
  bids: OrderBookLevel[]
  asks: OrderBookLevel[]
}

interface OrderBookProps {
  data: OrderBookData | null
  label: string
  color: string
  pricePrecision?: number
  qtyPrecision?: number
}

export function OrderBook({ data, label, color, pricePrecision = 4, qtyPrecision = 0 }: OrderBookProps) {
  const { asks, bids, maxQty, spread, midPrice } = useMemo(() => {
    if (!data || !data.asks.length || !data.bids.length) {
      return { asks: [], bids: [], maxQty: 1, spread: null, midPrice: null }
    }
    // asks: low to high (show reversed so highest ask on top)
    const asksSorted = [...data.asks].sort((a, b) => a.price - b.price).slice(0, 5)
    // bids: high to low
    const bidsSorted = [...data.bids].sort((a, b) => b.price - a.price).slice(0, 5)

    const allQty = [...asksSorted, ...bidsSorted].map(l => l.quantity)
    const maxQ = Math.max(...allQty, 1)

    const bestBid = bidsSorted[0]?.price ?? 0
    const bestAsk = asksSorted[0]?.price ?? 0
    const sp = bestAsk > 0 && bestBid > 0 ? bestAsk - bestBid : null
    const mid = bestAsk > 0 && bestBid > 0 ? (bestAsk + bestBid) / 2 : null

    return {
      asks: asksSorted.reverse(), // reversed: highest ask at top
      bids: bidsSorted,
      maxQty: maxQ,
      spread: sp,
      midPrice: mid,
    }
  }, [data])

  const rowHeight = 28

  return (
    <div style={{
      background: 'var(--bg-panel)',
      border: '1px solid var(--border)',
      borderRadius: 14,
      overflow: 'hidden',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: '10px 16px',
        borderBottom: '1px solid var(--border)',
      }}>
        <span style={{ fontSize: 13, fontWeight: 600, color }}>
          {label}
        </span>
        {midPrice !== null && (
          <span style={{ fontSize: 11, color: 'var(--text-dim)', fontFamily: 'var(--font-mono)' }}>
            mid: {midPrice.toFixed(pricePrecision)}
          </span>
        )}
      </div>

      {/* Column headers */}
      <div style={{
        display: 'grid', gridTemplateColumns: '1fr 1fr',
        padding: '6px 16px',
        borderBottom: '1px solid var(--border)',
        fontSize: 10,
        fontWeight: 600,
        color: 'var(--text-dim)',
        textTransform: 'uppercase',
        letterSpacing: '0.08em',
      }}>
        <span>Price</span>
        <span style={{ textAlign: 'right' }}>Qty</span>
      </div>

      {!data ? (
        <div style={{ padding: 24, textAlign: 'center', color: 'var(--text-dim)', fontSize: 13 }}>
          Waiting for data...
        </div>
      ) : (
        <>
          {/* Asks (red, highest at top) */}
          <div>
            {asks.map((level, i) => (
              <div key={`ask-${i}`} style={{
                display: 'grid',
                gridTemplateColumns: '1fr 1fr',
                padding: '0 16px',
                height: rowHeight,
                alignItems: 'center',
                position: 'relative',
              }}>
                {/* Quantity bar (right-aligned, red) */}
                <div style={{
                  position: 'absolute',
                  right: 0,
                  top: 0,
                  bottom: 0,
                  width: `${(level.quantity / maxQty) * 50}%`,
                  background: 'rgba(255, 71, 87, 0.08)',
                  transition: 'width 0.15s ease-out',
                }} />
                <span style={{
                  fontSize: 13,
                  fontFamily: 'var(--font-mono)',
                  fontWeight: 500,
                  color: 'var(--accent-red)',
                  position: 'relative',
                }}>
                  {level.price.toFixed(pricePrecision)}
                </span>
                <span style={{
                  fontSize: 13,
                  fontFamily: 'var(--font-mono)',
                  textAlign: 'right',
                  color: 'var(--text-secondary)',
                  position: 'relative',
                }}>
                  {level.quantity.toFixed(qtyPrecision)}
                </span>
              </div>
            ))}
          </div>

          {/* Spread divider */}
          <div style={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            padding: '6px 16px',
            borderTop: '1px solid var(--border)',
            borderBottom: '1px solid var(--border)',
            background: 'var(--bg-card)',
          }}>
            {spread !== null ? (
              <span style={{
                fontSize: 12,
                fontFamily: 'var(--font-mono)',
                fontWeight: 600,
                color: 'var(--text-secondary)',
              }}>
                Spread: {spread.toFixed(pricePrecision)}
              </span>
            ) : (
              <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>—</span>
            )}
          </div>

          {/* Bids (green, highest at top) */}
          <div>
            {bids.map((level, i) => (
              <div key={`bid-${i}`} style={{
                display: 'grid',
                gridTemplateColumns: '1fr 1fr',
                padding: '0 16px',
                height: rowHeight,
                alignItems: 'center',
                position: 'relative',
              }}>
                {/* Quantity bar (right-aligned, green) */}
                <div style={{
                  position: 'absolute',
                  right: 0,
                  top: 0,
                  bottom: 0,
                  width: `${(level.quantity / maxQty) * 50}%`,
                  background: 'rgba(0, 210, 106, 0.08)',
                  transition: 'width 0.15s ease-out',
                }} />
                <span style={{
                  fontSize: 13,
                  fontFamily: 'var(--font-mono)',
                  fontWeight: 500,
                  color: 'var(--accent-green)',
                  position: 'relative',
                }}>
                  {level.price.toFixed(pricePrecision)}
                </span>
                <span style={{
                  fontSize: 13,
                  fontFamily: 'var(--font-mono)',
                  textAlign: 'right',
                  color: 'var(--text-secondary)',
                  position: 'relative',
                }}>
                  {level.quantity.toFixed(qtyPrecision)}
                </span>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
