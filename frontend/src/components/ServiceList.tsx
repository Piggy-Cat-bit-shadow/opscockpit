import type { Service } from '@/types'
import { sortServices } from '@/types'
import { STATUS_LABEL, STATUS_COLORS, COLORS } from '@/theme'
import { formatBytes } from '@/types'

interface Props {
  services: Service[]
  selectedId?: string
  onSelect: (serviceId: string) => void
}

export function ServiceList({ services, selectedId, onSelect }: Props) {
  const sorted = sortServices(services)

  return (
    <div className="oc-servicelist">
      <div className="oc-panel-title">Services</div>
      <div className="oc-servicelist-scroll">
        {sorted.map((s) => {
          const color = STATUS_COLORS[s.status] ?? COLORS.grey
          const active = s.id === selectedId
          return (
            <button
              key={s.id}
              className={`oc-svcrow ${active ? 'oc-svcrow-active' : ''}`}
              onClick={() => onSelect(s.id)}
            >
              <span className="oc-svcrow-dot" style={{ background: color }} />
              <span className="oc-svcrow-name">{s.name}</span>
              <span className="oc-svcrow-right">
                <span className="oc-svcrow-ram">{s.memory?.rss_bytes ? formatBytes(s.memory.rss_bytes) : ''}</span>
                <span className="oc-svcrow-status" style={{ color }}>
                  {STATUS_LABEL[s.status]}
                </span>
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
