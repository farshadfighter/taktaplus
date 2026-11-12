import { useCallback, useEffect, useState } from 'react'
import { api } from './api'

// useFetch loads a GET endpoint and exposes reload (with the loading state)
// and refresh (in the background, for live updates).
export function useFetch<T>(path: string | null) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(path !== null)

  const reload = useCallback(async () => {
    if (path === null) return
    setLoading(true)
    try {
      setData(await api.get<T>(path))
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [path])

  const refresh = useCallback(async () => {
    if (path === null) return
    try {
      setData(await api.get<T>(path))
      setError(null)
    } catch {
      // keep what is shown; the next event or reload tries again
    }
  }, [path])

  useEffect(() => {
    void reload()
  }, [reload])

  return { data, error, loading, reload, refresh, setData }
}
