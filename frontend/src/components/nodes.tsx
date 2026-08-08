import { memo } from 'react'
import type { NodeProps, Node as RFNode } from '@xyflow/react'
import { Cpu, Radio, Braces } from 'lucide-react'
import type { FocusNode } from '@/topology'
import { COLORS, dotColor } from '@/theme'

type N = NodeProps<RFNode<FocusNode>>

// Service icons by service_id — only a glyph lookup. The UI still renders any
// service_id; unknown services get a neutral icon. No ports/units/paths are
// hardcoded.
const SERVICE_ICONS: Record<string, typeof Cpu> = {
  nginx: Cpu,
  hysteria2: Radio,
  tuic: Radio,
  'sing-box': Radio,
  xray: Cpu,
  'adguard-home': Cpu,
}

function resolveServiceIcon(serviceId?: string) {
  if (serviceId && SERVICE_ICONS[serviceId]) return SERVICE_ICONS[serviceId]
  return Cpu
}

export const PortNode = memo(({ data }: N) => (
  <div className="oc-fn-port" style={{ borderColor: COLORS.border }}>
    <span className="oc-fn-port-num">{data.label}</span>
    {data.exposure && (
      <span className="oc-fn-port-meta">{data.exposure === 'nat_ingress' ? 'NAT ingress' : data.exposure}</span>
    )}
  </div>
))

export const ProtocolNode = memo(({ data }: N) => (
  <div className="oc-fn-protocol" style={{ borderColor: COLORS.border }}>
    <Braces size={12} style={{ color: COLORS.textDim }} />
    <span className="oc-fn-protocol-label">{data.label}</span>
  </div>
))

export const ServiceNode = memo(({ data }: N) => {
  const Icon = resolveServiceIcon(data.serviceId)
  return (
    <div className="oc-fn-service" style={{ borderColor: COLORS.border }}>
      <span className="oc-fn-service-dot" style={{ background: dotColor(data.status) }} />
      <span className="oc-fn-service-icon">
        <Icon size={15} style={{ color: COLORS.text }} />
      </span>
      <span className="oc-fn-service-name">{data.label}</span>
    </div>
  )
})

export const nodeTypes = {
  port: PortNode,
  protocol: ProtocolNode,
  service: ServiceNode,
}
