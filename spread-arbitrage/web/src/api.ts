const BASE = '/api'

export interface Task {
  id: string
  symbol: string
  exchange_a: string
  exchange_b: string
  direction: 'long_spread' | 'short_spread'
  status: 'running' | 'stopped'
  open_threshold: number
  close_threshold: number
  confirm_count: number
  quantity_per_order: number
  max_position_qty: number
  data_max_latency_ms: number
  position_qty?: number
}

export interface TaskCreateRequest {
  symbol: string
  exchange_a: string
  exchange_b: string
  direction: 'long_spread' | 'short_spread'
  open_threshold: number
  close_threshold: number
  confirm_count: number
  quantity_per_order: number
  max_position_qty: number
  data_max_latency_ms: number
}

export interface Position {
  exchange: string
  symbol: string
  side: string
  size: number
  entry_price: number
  unrealized_pnl: number
}

export interface Trade {
  exchange: string
  symbol: string
  side: string
  quantity: number
  price: number
  fee: number
  timestamp: string
  order_id: string
}

export async function listTasks(): Promise<Task[]> {
  const res = await fetch(`${BASE}/tasks`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function createTask(req: TaskCreateRequest): Promise<Task> {
  const res = await fetch(`${BASE}/tasks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function updateTask(id: string, req: TaskCreateRequest): Promise<Task> {
  const res = await fetch(`${BASE}/tasks/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function deleteTask(id: string): Promise<void> {
  const res = await fetch(`${BASE}/tasks/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(await res.text())
}

export async function startTask(id: string): Promise<void> {
  const res = await fetch(`${BASE}/tasks/${id}/start`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text())
}

export async function stopTask(id: string): Promise<void> {
  const res = await fetch(`${BASE}/tasks/${id}/stop`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text())
}

export async function manualOpen(id: string): Promise<void> {
  const res = await fetch(`${BASE}/tasks/${id}/open`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text())
}

export async function manualClose(id: string): Promise<void> {
  const res = await fetch(`${BASE}/tasks/${id}/close`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text())
}

export async function getPositions(id: string): Promise<Position[]> {
  const res = await fetch(`${BASE}/tasks/${id}/positions`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function getTrades(id: string): Promise<Trade[]> {
  const res = await fetch(`${BASE}/tasks/${id}/trades`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function subscribe(exchange: string, symbol: string): Promise<void> {
  const res = await fetch(`${BASE}/subscribe`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ exchange, symbol }),
  })
  if (!res.ok) throw new Error(await res.text())
}

export async function unsubscribe(exchange: string, symbol: string): Promise<void> {
  const res = await fetch(`${BASE}/unsubscribe`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ exchange, symbol }),
  })
  if (!res.ok) throw new Error(await res.text())
}

export async function getPositionByExchange(exchange: string, symbol: string): Promise<Position> {
  const res = await fetch(`${BASE}/positions/${exchange}/${symbol}`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function getTradesByExchange(exchange: string, symbol: string): Promise<Trade[]> {
  const res = await fetch(`${BASE}/trades/${exchange}/${symbol}`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}
