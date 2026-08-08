import { Cpu, MemoryStick, HardDrive, Server, Clock } from 'lucide-react'
import type { State } from '@/types'
import { formatBytes, formatDuration } from '@/types'
import { COLORS, STATUS_COLORS } from '@/theme'

interface Props {
  state: State
}

function Meter({ label, value, percent, color }: { label: string; value: string; percent: number; color: string }) {
  return (
    <div className="oc-meter">
      <div className="oc-meter-top">
        <span className="oc-meter-label">{label}</span>
        <span className="oc-meter-value">{value}</span>
      </div>
      <div className="oc-meter-bar">
        <div className="oc-meter-fill" style={{ width: `${Math.min(100, Math.max(0, percent))}%`, background: color }} />
      </div>
    </div>
  )
}

export function HostSummary({ state }: Props) {
  const h = state.host
  const healthColor = STATUS_COLORS[state.health.status] ?? COLORS.grey
  const load = h.load?.load1 ?? 0

  return (
    <div className="oc-host">
      <div className="oc-host-name">
        <Server size={14} style={{ color: COLORS.textDim }} />
        <span className="oc-host-hostname">{h.hostname || 'unknown host'}</span>
        <span
          className="oc-host-status"
          style={{ color: healthColor, borderColor: `${healthColor}55`, background: `${healthColor}14` }}
        >
          {state.health.status}
        </span>
      </div>

      <div className="oc-host-meters">
        <Meter label="CPU" value={`${(h.cpu?.percent ?? 0).toFixed(0)}%`} percent={h.cpu?.percent ?? 0} color={COLORS.green} />
        <Meter label="RAM" value={formatBytes(h.memory?.used_bytes ?? 0)} percent={h.memory?.percent ?? 0} color={COLORS.green} />
        <Meter label="Disk" value={formatBytes(h.disk?.used_bytes ?? 0)} percent={h.disk?.percent ?? 0} color={COLORS.green} />
        <Meter label="Swap" value={formatBytes(h.swap?.used_bytes ?? 0)} percent={h.swap?.percent ?? 0} color={COLORS.green} />
      </div>

      <div className="oc-host-foot">
        <span className="oc-host-meta">
          <Clock size={11} style={{ color: COLORS.textDim }} /> up {formatDuration(h.uptime_seconds ?? 0)}
        </span>
        <span className="oc-host-meta">
          <Cpu size={11} style={{ color: COLORS.textDim }} /> {h.cpu?.cores ?? '—'} cores
        </span>
        <span className="oc-host-meta">
          <MemoryStick size={11} style={{ color: COLORS.textDim }} /> load {load.toFixed(2)}
        </span>
        <span className="oc-host-meta">
          <HardDrive size={11} style={{ color: COLORS.textDim }} /> {h.disk?.mount_point || '/'}
        </span>
      </div>
    </div>
  )
}
