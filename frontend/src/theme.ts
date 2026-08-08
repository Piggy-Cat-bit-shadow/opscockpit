import type { CSSProperties } from 'react'
import type { Status } from '@/types'

// Restrained dark palette — dark grey, minimal chrome, small green accents.
export const COLORS = {
  bg: '#0d0f12',
  panel: '#14171c',
  panelAlt: '#1a1e24',
  border: '#262b33',
  text: '#e6e9ee',
  textDim: '#8b939e',
  green: '#34c98a',
  yellow: '#d9a441',
  red: '#e5554e',
  grey: '#6b7280',
} as const

export const STATUS_COLORS: Record<Status, string> = {
  healthy: COLORS.green,
  warning: COLORS.yellow,
  failed: COLORS.red,
  unknown: COLORS.grey,
  stale: COLORS.yellow,
}

export const STATUS_LABEL: Record<Status, string> = {
  healthy: 'Healthy',
  warning: 'Warning',
  failed: 'Failed',
  unknown: 'Unknown',
  stale: 'Stale',
}

export function statusStyle(status: Status): CSSProperties {
  const c = STATUS_COLORS[status] ?? COLORS.grey
  return { color: c, borderColor: `${c}55`, backgroundColor: `${c}14` }
}

export function dotColor(status?: Status): string {
  return (status && STATUS_COLORS[status]) || COLORS.grey
}
