import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { Service, State } from '@/types'
import { formatBytes } from '@/types'
import { buildIngressBoard } from '@/topology'
import { STATUS_LABEL, STATUS_COLORS } from '@/theme'

interface Props {
  state: State
  selectedService?: Service | null
  onSelectService: (serviceId: string) => void
  onCloseDetail: () => void
  onRestart: (serviceId: string) => Promise<void>
}

function MobileDetail({ service, onRestart, onClose }: { service: Service; onRestart: (id: string) => Promise<void>; onClose: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
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

export function MobileTopology({ state, selectedService, onSelectService, onCloseDetail, onRestart }: Props) {
  const cards = useMemo(() => buildIngressBoard(state.topology), [state.topology])
  const [open, setOpen] = useState<Record<string, boolean>>({})

  if (cards.length === 0) {
    return <div className="oc-tree-empty">No public ingress discovered.</div>
  }

  return (
    <div className="oc-mobile-flow">
      {cards.map((c) => {
        const isOpen = !!open[c.label]
        return (
          <div key={c.label} className="oc-mcard">
            <button className="oc-mcard-head" onClick={() => setOpen((o) => ({ ...o, [c.label]: !o[c.label] }))}>
              {isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              <span className="oc-mcard-port">{c.label}</span>
              {c.exposure && <span className="oc-card-exposure">{c.exposure === 'nat_ingress' ? 'NAT' : c.exposure}</span>}
            </button>
            {isOpen && (
              <div className="oc-mcard-body">
                {c.protocols.map((p) => (
                  <div key={p.protocol} className="oc-mproto">
                    <span className={`oc-card-proto-badge oc-card-proto-${p.protocol}`}>{p.protocol.toUpperCase()}</span>
                    <div className="oc-mproto-services">
                      {p.natTarget != null && <span className="oc-card-nat">NAT → {p.natTarget}</span>}
                      {p.serviceIds.map((id) => {
                        const svc = state.services.find((s) => s.id === id)
                        if (!svc) return null
                        return (
                          <button key={id} className="oc-mproto-svc" onClick={() => onSelectService(id)}>
                            <span className="oc-tree-dot" style={{ background: STATUS_COLORS[svc.status] ?? '#6b7280' }} />
                            {svc.name}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        )
      })}
      {selectedService && <MobileDetail service={selectedService} onRestart={onRestart} onClose={onCloseDetail} />}
    </div>
  )
}
