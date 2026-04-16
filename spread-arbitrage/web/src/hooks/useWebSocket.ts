import { useEffect, useRef, useCallback, useState } from 'react'

export interface WSEvent {
  type: 'spread_update' | 'task_status' | 'trade_executed' | 'error' | 'quote'
  task_id: string
  data: any
}

export function useWebSocket(onMessage: (event: WSEvent) => void) {
  const [connected, setConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | undefined>(undefined)
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage

  const connect = useCallback(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws`)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => {
      setConnected(false)
      reconnectTimer.current = window.setTimeout(connect, 3000)
    }
    ws.onmessage = (e) => {
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

  return { connected }
}
