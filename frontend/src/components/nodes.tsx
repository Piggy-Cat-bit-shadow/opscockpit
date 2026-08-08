import { memo } from 'react'
import type { NodeProps, Node as RFNode } from '@xyflow/react'
import { Globe, Braces, Radio, Cpu } from 'lucide-react'
import type { TopoNode } from '@/types'
import { COLORS, dotColor } from '@/theme'

type N = NodeProps<RFNode<TopoNode>>

// Service icons by service_id — only used to *look up* an icon glyph. The
// frontend still renders whatever service_id arrives; unknown services get a
// neutral icon. Nothing about ports, units or paths is hardcoded.
const SERVICE_ICONS: Record<string, typeof Cpu> = {
  nginx: Globe,
  hysteria2: Radio,
  tuic: Radio,
  xray: Cpu,
  'adguard-home': Cpu,
}

function resolveServiceIcon(serviceId?: string) {
  if (serviceId && SERVICE_ICONS[serviceId]) return SERVICE_ICONS[serviceId]
  return Cpu
}

export const InternetNode = memo(({ data }: N) => (
  <div className="oc-node oc-node-internet" style={{ borderColor: COLORS.green + '66' }}>
    <Globe size={14} style={{ color: COLORS.green }} />
    <span className="oc-node-label">{data.label}</span>
  </div>
))

export const PortNode = memo(({ data }: N) => (
  <div className="oc-node oc-node-port">
    <span className="oc-node-port-num">{data.label}</span>
  </div>
))

export const ProtocolNode = memo(({ data }: N) => (
  <div className="oc-node oc-node-protocol">
    <Braces size={12} style={{ color: COLORS.textDim }} />
    <span className="oc-node-label">{data.label}</span>
  </div>
))

export const ServiceNode = memo(({ data }: N) => {
  const Icon = resolveServiceIcon(data.service_id)
  return (
    <div className="oc-node oc-node-service">
      <span className="oc-node-service-dot" style={{ background: dotColor(data.status) }} />
      <span className="oc-node-service-icon">
        <Icon size={14} style={{ color: COLORS.text }} />
      </span>
      <span className="oc-node-label">{data.label}</span>
    </div>
  )
})

export const nodeTypes = {
  internet: InternetNode,
  port: PortNode,
  protocol: ProtocolNode,
  service: ServiceNode,
}
