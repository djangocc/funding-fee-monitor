import type { Toast } from '../hooks/useToast'

const TYPE_STYLES: Record<Toast['type'], { bg: string; border: string; color: string }> = {
  success: { bg: 'rgba(0,210,106,0.12)', border: 'rgba(0,210,106,0.3)', color: 'var(--accent-green)' },
  error:   { bg: 'rgba(255,71,87,0.12)', border: 'rgba(255,71,87,0.3)', color: 'var(--accent-red)' },
  warning: { bg: 'rgba(255,184,0,0.12)', border: 'rgba(255,184,0,0.3)', color: 'var(--accent-amber)' },
  info:    { bg: 'rgba(61,122,255,0.12)', border: 'rgba(61,122,255,0.3)', color: 'var(--accent-blue)' },
}

interface ToastContainerProps {
  toasts: Toast[]
  onRemove: (id: number) => void
}

export function ToastContainer({ toasts, onRemove }: ToastContainerProps) {
  if (toasts.length === 0) return null

  return (
    <div style={{
      position: 'fixed',
      top: 60,
      right: 20,
      zIndex: 9999,
      display: 'flex',
      flexDirection: 'column',
      gap: 8,
      maxWidth: 400,
    }}>
      {toasts.map(toast => {
        const s = TYPE_STYLES[toast.type]
        return (
          <div
            key={toast.id}
            style={{
              background: s.bg,
              border: `1px solid ${s.border}`,
              borderRadius: 10,
              padding: '12px 16px',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-start',
              gap: 12,
              animation: 'fadeIn 0.2s ease-out',
              backdropFilter: 'blur(12px)',
              boxShadow: '0 8px 32px rgba(0,0,0,0.4)',
            }}
          >
            <span style={{
              fontSize: 13,
              fontFamily: 'var(--font-display)',
              color: s.color,
              fontWeight: 500,
              lineHeight: 1.5,
              whiteSpace: 'pre-line',
            }}>
              {toast.message}
            </span>
            <button
              onClick={() => onRemove(toast.id)}
              style={{
                background: 'none',
                border: 'none',
                color: s.color,
                cursor: 'pointer',
                fontSize: 16,
                padding: 0,
                lineHeight: 1,
                opacity: 0.6,
                flexShrink: 0,
              }}
            >&times;</button>
          </div>
        )
      })}
    </div>
  )
}
