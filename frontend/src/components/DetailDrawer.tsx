import { useState } from 'react'
import { X, Copy, Check, RotateCw } from 'lucide-react'
import type { Service } from '@/types'
import { STATUS_LABEL, STATUS_COLORS } from '@/theme'
import { formatBytes } from '@/types'

interface Props {
  service: Service | null
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
    <div className="oc-drawer-row">
      <span className="oc-drawer-label">{label}</span>
      <div className="oc-drawer-value">
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

export function DetailDrawer({ service, onClose, onRestart }: Props) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (!service) return null

  const color = STATUS_COLORS[service.status] ?? '#6b7280'
  const version = service.version || '—'
  const memory = service.memory ? formatBytes(service.memory.rss_bytes) : '—'
  const canRestart = !!service.restart_enabled && !!service.unit

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
    <div className="oc-drawer">
      <div className="oc-drawer-head">
        <span className="oc-drawer-name">{service.name}</span>
        <button className="oc-iconbtn" onClick={onClose} aria-label="close">
          <X size={14} />
        </button>
      </div>

      <div className="oc-drawer-status">
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

      <div className="oc-drawer-rows">
        <div className="oc-drawer-row">
          <span className="oc-drawer-label">Version</span>
          <span className="oc-mono">{version}</span>
        </div>
        <div className="oc-drawer-row">
          <span className="oc-drawer-label">Memory</span>
          <span>{memory}</span>
        </div>
        {service.unit && <CopyRow label="Unit" value={service.unit} mono />}
        {service.config_path && <CopyRow label="Config" value={service.config_path} mono />}
      </div>

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
  )
}
