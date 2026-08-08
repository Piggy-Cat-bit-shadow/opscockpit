import type { Healthz, State } from '@/types'

const API_BASE = ''

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function fetchState(etag?: string): Promise<{ state: State | null; etag?: string; notModified: boolean }> {
  const headers: Record<string, string> = {}
  if (etag) headers['If-None-Match'] = etag

  const res = await fetch(`${API_BASE}/api/state`, { headers })
  if (res.status === 304) {
    return { state: null, notModified: true }
  }
  if (!res.ok) {
    throw new ApiError(res.status, `state request failed: ${res.status}`)
  }
  const nextEtag = res.headers.get('ETag') ?? undefined
  const state = (await res.json()) as State
  return { state, etag: nextEtag, notModified: false }
}

export async function fetchHealthz(): Promise<Healthz> {
  const res = await fetch(`${API_BASE}/api/healthz`)
  if (!res.ok) {
    throw new ApiError(res.status, `healthz failed: ${res.status}`)
  }
  return (await res.json()) as Healthz
}

export async function restartService(serviceId: string): Promise<void> {
  const res = await fetch(`${API_BASE}/api/services/${encodeURIComponent(serviceId)}/restart`, {
    method: 'POST',
    headers: { 'X-OpsCockpit-Action': 'restart' },
  })
  if (!res.ok) {
    throw new ApiError(res.status, `restart failed: ${res.status}`)
  }
}

export const POLL_FOREGROUND_MS = 8000
export const POLL_BACKGROUND_MS = 45000

export interface PollController {
  /** Start polling; returns a cleanup function. */
  subscribe: () => () => void
  /** Force an immediate poll now. */
  refresh: () => void
}

/**
 * Creates a polling controller for GET /api/state.
 * Foreground interval 8s; while the tab is hidden, 45s. ETag-aware (304s
 * short-circuit without a body). No WebSocket anywhere.
 */
export function createPollController(
  onData: (state: State) => void,
  onError: (err: unknown) => void,
): PollController {
  let etag: string | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = true

  const poll = () => {
    if (stopped) return
    const isHidden = document.visibilityState === 'hidden'
    fetchState(etag)
      .then(({ state, etag: next }) => {
        if (next) etag = next
        if (state) onData(state)
      })
      .catch((err) => onError(err))
      .finally(() => {
        if (!stopped) {
          timer = setTimeout(poll, isHidden ? POLL_BACKGROUND_MS : POLL_FOREGROUND_MS)
        }
      })
  }

  const refresh = () => {
    if (timer) clearTimeout(timer)
    poll()
  }

  const onVisibility = () => {
    if (document.visibilityState === 'visible') refresh()
  }

  return {
    subscribe: () => {
      stopped = false
      document.addEventListener('visibilitychange', onVisibility)
      poll()
      return () => {
        stopped = true
        if (timer) clearTimeout(timer)
        document.removeEventListener('visibilitychange', onVisibility)
      }
    },
    refresh,
  }
}
