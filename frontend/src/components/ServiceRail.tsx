import { useMemo, useState } from 'react'
import type { Service } from '@/types'
import { sortServices, formatBytes } from '@/types'
import { STATUS_COLORS } from '@/theme'

interface Props {
  services: Service[]
  selectedId?: string
  onSelect: (serviceId: string) => void
}

export function ServiceRail({ services, selectedId, onSelect }: Props) {
  const [query, setQuery] = useState('')
  const sorted = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return sortServices(services)
    return sortServices(services).filter((s) => s.name.toLowerCase().includes(q) || s.id.toLowerCase().includes(q))
  }, [services, query])

  return (
    <div className="oc-rail">
      <div className="oc-rail-search">
        <input
          className="oc-rail-input"
          placeholder="Search services…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="search services"
        />
      </div>
      <div className="oc-rail-scroll">
        {sorted.map((s) => {
          const color = STATUS_COLORS[s.status] ?? '#6b7280'
          const active = s.id === selectedId
          return (
            <button key={s.id} className={`oc-rail-row ${active ? 'oc-rail-row-active' : ''}`} onClick={() => onSelect(s.id)}>
              <span className="oc-svcrow-dot" style={{ background: color }} />
              <span className="oc-rail-name">{s.name}</span>
              <span className="oc-rail-ram">{s.memory?.rss_bytes ? formatBytes(s.memory.rss_bytes) : ''}</span>
            </button>
          )
        })}
        {sorted.length === 0 && <div className="oc-dim oc-rail-empty">No matching services</div>}
      </div>
    </div>
  )
}
