import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { State } from '@/types'
import { buildPortTree, formatBytes } from '@/types'
import { STATUS_LABEL, STATUS_COLORS } from '@/theme'

interface Props {
  state: State
  selectedId?: string
  onSelectService: (serviceId: string) => void
}

function ServiceLeaf({
  name,
  status,
  onSelect,
  active,
}: {
  name: string
  status?: string
  onSelect: () => void
  active: boolean
}) {
  const color = (status && STATUS_COLORS[status as keyof typeof STATUS_COLORS]) || '#6b7280'
  return (
    <button className={`oc-tree-leaf ${active ? 'oc-tree-leaf-active' : ''}`} onClick={onSelect}>
      <span className="oc-tree-dot" style={{ background: color }} />
      <span className="oc-tree-leaf-name">{name}</span>
    </button>
  )
}

export function MobileTopology({ state, selectedId, onSelectService }: Props) {
  const tree = useMemo(() => buildPortTree(state.topology), [state.topology])
  const [openPorts, setOpenPorts] = useState<Set<string>>(() => new Set(tree.map((p) => p.label)))
  const [openProtos, setOpenProtos] = useState<Set<string>>(() => new Set())

  const togglePort = (label: string) => {
    setOpenPorts((prev) => {
      const next = new Set(prev)
      if (next.has(label)) next.delete(label)
      else next.add(label)
      return next
    })
  }

  const toggleProto = (key: string) => {
    setOpenProtos((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  if (tree.length === 0) {
    return <div className="oc-tree-empty">No public services listening.</div>
  }

  return (
    <div className="oc-tree">
      <div className="oc-tree-internet">Internet</div>
      {tree.map((p) => {
        const open = openPorts.has(p.label)
        return (
          <div key={p.label} className="oc-tree-port">
            <button className="oc-tree-port-head" onClick={() => togglePort(p.label)}>
              {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
              <span className="oc-tree-port-num">{p.label}</span>
            </button>
            {open &&
              p.protocols.map((pr) => {
                const key = `${p.label}-${pr.protocol}`
                const openP = openProtos.has(key)
                return (
                  <div key={key} className="oc-tree-proto">
                    <button className="oc-tree-proto-head" onClick={() => toggleProto(key)}>
                      {openP ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                      <span className="oc-tree-proto-label">{pr.protocol.toUpperCase()}</span>
                    </button>
                    {openP &&
                      pr.services.map((s) => (
                        <ServiceLeaf
                          key={s.instanceId}
                          name={s.name}
                          status={s.status}
                          active={s.serviceId === selectedId}
                          onSelect={() => onSelectService(s.serviceId)}
                        />
                      ))}
                  </div>
                )
              })}
          </div>
        )
      })}
    </div>
  )
}

export function BottomSheet({
  service,
  onClose,
  onRestart,
}: {
  service: NonNullable<State['services'][number]> | null
  onClose: () => void
  onRestart: (serviceId: string) => Promise<void>
}) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (!service) return null

  const color = STATUS_COLORS[service.status] ?? '#6b7280'
  const doRestart = async () => {
    setBusy(true)
    setError(null)
    try {
      await onRestart(service.id)
      setConfirming(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'restart failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="oc-sheet-backdrop" onClick={onClose}>
      <div className="oc-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="oc-sheet-grabber" />
        <div className="oc-sheet-head">
          <span className="oc-sheet-name">{service.name}</span>
          <span className="oc-statusbadge" style={{ color, borderColor: `${color}55`, background: `${color}14` }}>
            {STATUS_LABEL[service.status]}
          </span>
        </div>
        {service.health?.problems?.map((p, i) => (
          <div key={i} className="oc-problem">
            {p}
          </div>
        ))}
        <div className="oc-sheet-rows">
          <div className="oc-sheet-row">
            <span>Version</span>
            <span className="oc-mono">{service.version || '—'}</span>
          </div>
          <div className="oc-sheet-row">
            <span>Memory</span>
            <span>{service.memory ? formatBytes(service.memory.rss_bytes) : '—'}</span>
          </div>
          <div className="oc-sheet-row">
            <span>Unit</span>
            <span className="oc-mono oc-sheet-truncate">{service.unit || '—'}</span>
          </div>
          <div className="oc-sheet-row">
            <span>Config</span>
            <span className="oc-mono oc-sheet-truncate">{service.config_path || '—'}</span>
          </div>
        </div>
        {error && <div className="oc-drawer-error">{error}</div>}
        {service.restart_enabled && service.unit && (
          <div className="oc-sheet-restart">
            {confirming ? (
              <div className="oc-confirm">
                <div className="oc-confirm-text">
                  Confirm restart {service.name}?
                  <span className="oc-mono oc-dim">{service.unit}</span>
                </div>
                <div className="oc-confirm-actions">
                  <button className="oc-btn oc-btn-ghost" onClick={() => setConfirming(false)} disabled={busy}>
                    Cancel
                  </button>
                  <button className="oc-btn oc-btn-danger" onClick={doRestart} disabled={busy}>
                    {busy ? 'Restarting…' : 'Confirm restart'}
                  </button>
                </div>
              </div>
            ) : (
              <button className="oc-btn oc-btn-outline" onClick={() => setConfirming(true)}>
                Restart service
              </button>
            )}
          </div>
        )}
        <button className="oc-btn oc-btn-ghost oc-sheet-close" onClick={onClose}>
          Close
        </button>
      </div>
    </div>
  )
}
