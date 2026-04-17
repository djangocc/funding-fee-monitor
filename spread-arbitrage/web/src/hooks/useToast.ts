import { useState, useCallback, useRef } from 'react'

export interface Toast {
  id: number
  message: string
  type: 'success' | 'error' | 'warning' | 'info'
}

export function useToast() {
  const [toasts, setToasts] = useState<Toast[]>([])
  const idRef = useRef(0)

  const addToast = useCallback((message: string, type: Toast['type'] = 'info', durationMs = 5000) => {
    const id = ++idRef.current
    setToasts(prev => [...prev, { id, message, type }])
    setTimeout(() => {
      setToasts(prev => prev.filter(t => t.id !== id))
    }, durationMs)
  }, [])

  const removeToast = useCallback((id: number) => {
    setToasts(prev => prev.filter(t => t.id !== id))
  }, [])

  return { toasts, addToast, removeToast }
}

// Play a short beep sound using Web Audio API
export function playSound(type: 'open' | 'close' | 'error') {
  try {
    const ctx = new AudioContext()
    const osc = ctx.createOscillator()
    const gain = ctx.createGain()
    osc.connect(gain)
    gain.connect(ctx.destination)

    if (type === 'open') {
      // Rising tone: buy/open
      osc.frequency.setValueAtTime(600, ctx.currentTime)
      osc.frequency.linearRampToValueAtTime(900, ctx.currentTime + 0.15)
      gain.gain.setValueAtTime(0.3, ctx.currentTime)
      gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.2)
      osc.start()
      osc.stop(ctx.currentTime + 0.2)
    } else if (type === 'close') {
      // Falling tone: sell/close
      osc.frequency.setValueAtTime(900, ctx.currentTime)
      osc.frequency.linearRampToValueAtTime(600, ctx.currentTime + 0.15)
      gain.gain.setValueAtTime(0.3, ctx.currentTime)
      gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.2)
      osc.start()
      osc.stop(ctx.currentTime + 0.2)
    } else {
      // Error: two short beeps
      osc.frequency.setValueAtTime(400, ctx.currentTime)
      gain.gain.setValueAtTime(0.4, ctx.currentTime)
      gain.gain.setValueAtTime(0, ctx.currentTime + 0.1)
      gain.gain.setValueAtTime(0.4, ctx.currentTime + 0.15)
      gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.25)
      osc.start()
      osc.stop(ctx.currentTime + 0.3)
    }

    setTimeout(() => ctx.close(), 1000)
  } catch {
    // Audio not available
  }
}
