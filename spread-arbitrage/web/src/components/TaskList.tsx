import { useState } from 'react'
import type { Task, TaskCreateRequest } from '../api'
import { TaskDetail } from './TaskDetail'
import { TaskForm } from './TaskForm'

interface SpreadUpdate {
  spread: number
  open_counter: number
  close_counter: number
  bid_a: number
  ask_a: number
  bid_b: number
  ask_b: number
}

interface TaskListProps {
  tasks: Task[]
  spreads: Map<string, SpreadUpdate>
  onRefresh: () => void
  onCreateTask: (req: TaskCreateRequest) => void
  onUpdateTask: (id: string, req: TaskCreateRequest) => void
  onDeleteTask: (id: string) => void
  onStartTask: (id: string) => void
  onStopTask: (id: string) => void
  onManualOpen: (id: string) => void
  onManualClose: (id: string) => void
}

export function TaskList({
  tasks,
  spreads,
  onRefresh,
  onCreateTask,
  onUpdateTask,
  onDeleteTask,
  onStartTask,
  onStopTask,
  onManualOpen,
  onManualClose,
}: TaskListProps) {
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [editingTaskId, setEditingTaskId] = useState<string | null>(null)

  const handleCreate = (req: TaskCreateRequest) => {
    onCreateTask(req)
    setShowCreateForm(false)
  }

  const handleUpdate = (id: string, req: TaskCreateRequest) => {
    onUpdateTask(id, req)
    setEditingTaskId(null)
  }

  const handleDelete = (id: string) => {
    if (confirm('Are you sure you want to delete this task?')) {
      onDeleteTask(id)
    }
  }

  const buttonStyle = {
    padding: '10px 20px',
    background: '#4caf50',
    color: '#fff',
    border: 'none',
    borderRadius: '4px',
    cursor: 'pointer',
    fontSize: '14px',
    fontWeight: 'bold' as const,
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
        <button onClick={() => setShowCreateForm(!showCreateForm)} style={buttonStyle}>
          {showCreateForm ? 'Cancel' : '+ New Task'}
        </button>
        <button onClick={onRefresh} style={{ ...buttonStyle, background: '#2196f3' }}>
          Refresh
        </button>
      </div>

      {showCreateForm && (
        <div style={{ marginBottom: '20px' }}>
          <TaskForm onSubmit={handleCreate} onCancel={() => setShowCreateForm(false)} />
        </div>
      )}

      {tasks.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px', color: '#888' }}>
          No tasks yet. Click "New Task" to create one.
        </div>
      ) : (
        tasks.map((task) => (
          <div key={task.id}>
            {editingTaskId === task.id ? (
              <div style={{ marginBottom: '20px' }}>
                <TaskForm
                  onSubmit={(req) => handleUpdate(task.id, req)}
                  initialValues={task}
                  onCancel={() => setEditingTaskId(null)}
                />
              </div>
            ) : (
              <TaskDetail
                task={task}
                spreadData={spreads.get(task.id)}
                onStart={() => onStartTask(task.id)}
                onStop={() => onStopTask(task.id)}
                onManualOpen={() => onManualOpen(task.id)}
                onManualClose={() => onManualClose(task.id)}
                onEdit={() => setEditingTaskId(task.id)}
                onDelete={() => handleDelete(task.id)}
              />
            )}
          </div>
        ))
      )}
    </div>
  )
}
