import { useMemo, useState } from 'react'
import { X, Copy, Check, RotateCw } from 'lucide-react'
import type { Service, Topology } from '@/types'
import { formatBytes } from '@/types'
import { STATUS_LABEL, STATUS_COLORS } from '@/theme'
import { buildServiceFocus } from '@/topology'

interface Props {
  service: Service | null
  topology: Topology
  onClose: () => void
  onRestart: (serviceId: string) => Promise<void>
}

function CopyRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard unavailable */
    }
  }
  return (
    <div className="oc-dr-row">
      <span className="oc-dr-label">{label}</span>
      <div className="oc-dr-value">
        <span className={mono ? 'oc-mono' : ''} title={value}>
          {value}
        </span>
        <button className="oc-copybtn" onClick={copy} aria-label={`copy ${label}`}>
          {copied ? <Check size={11} /> : <Copy size={11} />}
        </button>
      </div>
    </div>
  )
}

/**
 * InspectorDrawer overlays the right side of the canvas (does NOT compress the
 * center topology width). Shows only the allowed details.
 */
export function InspectorDrawer({ service, topology, onClose, onRestart }: Props) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const focus = useMemo(() => (service ? buildServiceFocus(topology, service.id) : null), [topology, service])

  if (!service) return null

  const color = STATUS_COLORS[service.status] ?? '#6b7280'
  const version = service.version || '—'
  const memory = service.memory ? formatBytes(service.memory.rss_bytes) : '—'
  const canRestart = !!service.restart_enabled && !!service.unit

  // Entrypoints = port nodes in the reverse graph.
  const entrypoints = focus?.nodes.filter((n) => n.type === 'port').map((n) => n.label) ?? []
  const dependencies = focus?.nodes.filter((n) => n.type === 'service' && n.serviceId !== service.id).map((n) => n.label) ?? []

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
    <div className="oc-inspector">
      <div className="oc-inspector-backdrop" onClick={onClose} />
      <div className="oc-inspector-panel">
        <div className="oc-dr-head">
          <span className="oc-dr-name">{service.name}</span>
          <button className="oc-iconbtn" onClick={onClose} aria-label="close">
            <X size={14} />
          </button>
        </div>

        <div className="oc-dr-status">
          <span className="oc-statusbadge" style={{ color, borderColor: `${color}55`, background: `${color}14` }}>
            {STATUS_LABEL[service.status]}
          </span>
          {service.unit_state && <span className="oc-dim">{service.unit_state}</span>}
        </div>

        {service.health?.problems && service.health.problems.length > 0 && (
          <div className="oc-drawer-problems">
            {service.health.problems.map((p, i) => (
              <div key={i} className="oc-problem">
                {p}
              </div>
            ))}
          </div>
        )}

        <div className="oc-dr-rows">
          <div className="oc-dr-row">
            <span className="oc-dr-label">Version</span>
            <span className="oc-mono">{version}</span>
          </div>
          <div className="oc-dr-row">
            <span className="oc-dr-label">Memory</span>
            <span>{memory}</span>
          </div>
          {service.unit && <CopyRow label="Unit" value={service.unit} mono />}
          {service.config_path && <CopyRow label="Config" value={service.config_path} mono />}
        </div>

        {entrypoints.length > 0 && (
          <div className="oc-dr-section">
            <span className="oc-dr-section-label">Entrypoints</span>
            <span className="oc-dr-chipwrap">{entrypoints.map((p) => <span key={p} className="oc-chip">{p}</span>)}</span>
          </div>
        )}
        {dependencies.length > 0 && (
          <div className="oc-dr-section">
            <span className="oc-dr-section-label">Dependencies</span>
            <span className="oc-dr-chipwrap">{dependencies.map((p) => <span key={p} className="oc-chip">{p}</span>)}</span>
          </div>
        )}

        {error && <div className="oc-drawer-error">{error}</div>}

        {canRestart && (
          <div className="oc-drawer-restart">
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
                <RotateCw size={12} /> Restart service
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
