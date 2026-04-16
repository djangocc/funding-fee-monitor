import { useState, useEffect, useCallback } from 'react'
import { useWebSocket, type WSEvent } from './hooks/useWebSocket'
import * as api from './api'
import { TaskList } from './components/TaskList'

function App() {
  const [tasks, setTasks] = useState<api.Task[]>([])
  const [spreads, setSpreads] = useState<Map<string, any>>(new Map())

  const refreshTasks = useCallback(async () => {
    try { setTasks(await api.listTasks()) } catch (e) { console.error(e) }
  }, [])

  const handleWS = useCallback((event: WSEvent) => {
    if (event.type === 'spread_update') {
      setSpreads(prev => new Map(prev).set(event.task_id, event.data))
    }
    if (event.type === 'task_status' || event.type === 'trade_executed') {
      refreshTasks()
    }
    if (event.type === 'error') {
      console.error('[WS Error]', event)
      refreshTasks()
    }
  }, [refreshTasks])

  const { connected } = useWebSocket(handleWS)

  useEffect(() => { refreshTasks() }, [refreshTasks])

  const handleCreate = async (req: api.TaskCreateRequest) => {
    await api.createTask(req); refreshTasks()
  }
  const handleUpdate = async (id: string, req: api.TaskCreateRequest) => {
    await api.updateTask(id, req); refreshTasks()
  }
  const handleDelete = async (id: string) => {
    await api.deleteTask(id); refreshTasks()
  }
  const handleStart = async (id: string) => {
    await api.startTask(id); refreshTasks()
  }
  const handleStop = async (id: string) => {
    await api.stopTask(id); refreshTasks()
  }

  return (
    <div style={{ background: '#1e1e1e', color: '#e0e0e0', minHeight: '100vh', padding: '20px', fontFamily: 'monospace' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
        <h1 style={{ margin: 0 }}>Spread Arbitrage</h1>
        <span style={{ color: connected ? '#4caf50' : '#f44336' }}>
          WS: {connected ? 'Connected' : 'Disconnected'}
        </span>
      </div>
      <TaskList
        tasks={tasks}
        spreads={spreads}
        onRefresh={refreshTasks}
        onCreateTask={handleCreate}
        onUpdateTask={handleUpdate}
        onDeleteTask={handleDelete}
        onStartTask={handleStart}
        onStopTask={handleStop}
        onManualOpen={(id) => api.manualOpen(id).then(refreshTasks)}
        onManualClose={(id) => api.manualClose(id).then(refreshTasks)}
      />
    </div>
  )
}

export default App
