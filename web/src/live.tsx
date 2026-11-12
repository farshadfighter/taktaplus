import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { App as AntApp } from 'antd'
import { useNavigate } from 'react-router'

// Live events from /api/v1/events (Server-Sent Events). One EventSource is
// shared by the whole panel; pages subscribe to the event types they show.

type Handler = (type: string, data: Record<string, unknown>) => void
type Live = { subscribe: (types: string[], h: Handler) => () => void; connected: boolean }

const types = ['alert', 'change', 'device', 'backup', 'report', 'upgrade']
const LiveContext = createContext<Live | null>(null)

const severityFa: Record<string, string> = { critical: 'بحرانی', high: 'بالا' }

export function LiveProvider({ children }: { children: ReactNode }) {
  const handlers = useRef(new Map<Handler, Set<string>>())
  const [connected, setConnected] = useState(false)
  const { notification } = AntApp.useApp()
  const nav = useNavigate()
  const navRef = useRef(nav)
  navRef.current = nav

  useEffect(() => {
    const es = new EventSource('/api/v1/events')
    let wasDown = false
    const dispatch = (type: string, data: Record<string, unknown>) => {
      for (const [h, ts] of handlers.current) if (ts.has(type) || type === 'resync') h(type, data)
    }
    es.onopen = () => {
      setConnected(true)
      // After a reconnect, events may have been missed: refresh everything.
      if (wasDown) dispatch('resync', {})
      wasDown = false
    }
    es.onerror = () => {
      setConnected(false)
      wasDown = true
    }
    for (const t of [...types, 'resync']) {
      es.addEventListener(t, (ev) => {
        let data: Record<string, unknown> = {}
        try { data = JSON.parse((ev as MessageEvent).data) } catch { /* keep empty */ }
        dispatch(t, data)
        if (t === 'alert' && data.state === 'open' && (data.severity === 'critical' || data.severity === 'high')) {
          notification.open({
            key: `alert-${data.id}`,
            type: data.severity === 'critical' ? 'error' : 'warning',
            title: `هشدار ${severityFa[String(data.severity)]}: ${String(data.device ?? '')}`,
            description: `${String(data.rule ?? '')} — ${String(data.message ?? '')}`,
            placement: 'bottomLeft',
            duration: data.severity === 'critical' ? 0 : 8,
            onClick: () => navRef.current('/alerts'),
            style: { cursor: 'pointer' },
          })
        }
      })
    }
    es.addEventListener('signed_out', () => es.close())
    return () => es.close()
  }, [notification])

  const subscribe = (ts: string[], h: Handler) => {
    handlers.current.set(h, new Set(ts))
    return () => { handlers.current.delete(h) }
  }
  return <LiveContext.Provider value={{ subscribe, connected }}>{children}</LiveContext.Provider>
}

export function useLiveStatus() {
  return useContext(LiveContext)?.connected ?? false
}

// useLiveRefresh calls refresh (debounced) when one of the event types
// arrives and match, if given, accepts its data; and after a reconnect.
export function useLiveRefresh(ts: string[], refresh: () => unknown, match?: (data: Record<string, unknown>) => boolean) {
  const live = useContext(LiveContext)
  const fn = useRef(refresh)
  const m = useRef(match)
  fn.current = refresh
  m.current = match
  const key = ts.join(',')
  useEffect(() => {
    if (!live) return
    let timer: ReturnType<typeof setTimeout> | undefined
    const off = live.subscribe(key.split(','), (type, data) => {
      if (type !== 'resync' && m.current && !m.current(data)) return
      clearTimeout(timer)
      timer = setTimeout(() => void fn.current(), 600)
    })
    return () => { off(); clearTimeout(timer) }
  }, [live, key])
}
