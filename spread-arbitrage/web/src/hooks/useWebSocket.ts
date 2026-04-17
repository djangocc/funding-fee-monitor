import { useEffect, useRef, useCallback, useState } from 'react'

export interface WSEvent {
  type: 'spread_update' | 'task_status' | 'trade_executed' | 'error' | 'quote' | 'orderbook'
  task_id: string
  data: any
}

export function useWebSocket(onMessage: (event: WSEvent) => void) {
  const [connected, setConnected] = useState(false)
  const [latencyMs, setLatencyMs] = useState<number | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | undefined>(undefined)
  const onMessageRef = useRef(onMessage)
  const lastMsgTime = useRef<number>(0)
  onMessageRef.current = onMessage

  const connect = useCallback(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws`)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => {
      setConnected(false)
      setLatencyMs(null)
      reconnectTimer.current = window.setTimeout(connect, 3000)
    }
    ws.onmessage = (e) => {
      lastMsgTime.current = Date.now()
      try {
        const event: WSEvent = JSON.parse(e.data)
        onMessageRef.current(event)
      } catch { /* ignore malformed */ }
    }
  }, [])

  useEffect(() => {
    connect()
    return () => {
      clearTimeout(reconnectTimer.current)
      wsRef.current?.close()
    }
  }, [connect])

  useEffect(() => {
    const iv = setInterval(() => {
      if (lastMsgTime.current > 0) {
        setLatencyMs(Date.now() - lastMsgTime.current)
      }
    }, 1000)
    return () => clearInterval(iv)
  }, [])

  return { connected, latencyMs }
}
