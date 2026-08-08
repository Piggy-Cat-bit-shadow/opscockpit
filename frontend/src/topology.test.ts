import { describe, it, expect } from 'vitest'
import { buildIngressBoard, buildPortFocus, buildServiceFocus } from '@/topology'
import type { Topology } from '@/types'

// A realistic topology: internet → ports → protocols → services → deps.
const topology: Topology = {
  nodes: [
    { id: 'internet', type: 'internet', label: 'Internet' },
    { id: 'port-443', type: 'port', label: '443', port: 443, port_start: 443, port_end: 443 },
    { id: 'port-443-tcp', type: 'protocol', label: 'TCP', protocol: 'tcp', port: 443, port_start: 443, port_end: 443 },
    { id: 'nginx@tcp:443', type: 'service', label: 'Nginx', service_id: 'nginx', protocol: 'tcp', port: 443, port_start: 443, port_end: 443, status: 'healthy' },
    { id: 'port-443-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 443, port_start: 443, port_end: 443 },
    { id: 'hysteria2@udp:443', type: 'service', label: 'Hysteria2', service_id: 'hysteria2', protocol: 'udp', port: 443, port_start: 443, port_end: 443, status: 'healthy' },
    { id: 'port-8554', type: 'port', label: '8554', port: 8554, port_start: 8554, port_end: 8554 },
    { id: 'port-8554-tcp', type: 'protocol', label: 'TCP', protocol: 'tcp', port: 8554, port_start: 8554, port_end: 8554 },
    { id: 'shadowtls@tcp:8554', type: 'service', label: 'ShadowTLS', service_id: 'shadowtls', protocol: 'tcp', port: 8554, port_start: 8554, port_end: 8554, status: 'healthy' },
    { id: 'snell@dep:tcp:17414', type: 'service', label: 'Snell', service_id: 'snell', protocol: 'tcp', status: 'healthy' },
    { id: 'port-20000-20099', type: 'port', label: '20000–20099', port: 20000, port_start: 20000, port_end: 20099, exposure: 'nat_ingress', target_port: 443 },
    { id: 'port-20000-20099-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 20000, port_start: 20000, port_end: 20099 },
    { id: 'hysteria2@udp:20000', type: 'service', label: 'Hysteria2', service_id: 'hysteria2', protocol: 'udp', port: 443, port_start: 20000, port_end: 20099, status: 'healthy', target_port: 443 },
  ],
  edges: [
    { id: 'e1', source: 'internet', target: 'port-443' },
    { id: 'e2', source: 'port-443', target: 'port-443-tcp' },
    { id: 'e3', source: 'port-443-tcp', target: 'nginx@tcp:443' },
    { id: 'e4', source: 'port-443', target: 'port-443-udp' },
    { id: 'e5', source: 'port-443-udp', target: 'hysteria2@udp:443' },
    { id: 'e6', source: 'internet', target: 'port-8554' },
    { id: 'e7', source: 'port-8554', target: 'port-8554-tcp' },
    { id: 'e8', source: 'port-8554-tcp', target: 'shadowtls@tcp:8554' },
    { id: 'e9', source: 'shadowtls@tcp:8554', target: 'snell@dep:tcp:17414' },
    { id: 'e10', source: 'internet', target: 'port-20000-20099' },
    { id: 'e11', source: 'port-20000-20099', target: 'port-20000-20099-udp' },
    { id: 'e12', source: 'port-20000-20099-udp', target: 'hysteria2@udp:443' },
  ],
}

describe('buildIngressBoard', () => {
  it('groups by port and protocol, deterministic ascending order', () => {
    const cards = buildIngressBoard(topology)
    expect(cards.map((c) => c.label)).toEqual(['443', '8554', '20000–20099'])
    const p443 = cards[0]
    expect(p443.protocols.map((p) => p.protocol)).toEqual(['tcp', 'udp'])
    expect(p443.protocols[0].services).toEqual(['Nginx'])
    expect(p443.protocols[1].services).toEqual(['Hysteria2'])
  })

  it('traces dependency chains on the card (ShadowTLS → Snell)', () => {
    const cards = buildIngressBoard(topology)
    const p8554 = cards.find((c) => c.label === '8554')!
    expect(p8554.protocols[0].services).toEqual(['ShadowTLS', 'Snell'])
  })

  it('shows NAT target on range cards', () => {
    const cards = buildIngressBoard(topology)
    const nat = cards.find((c) => c.label === '20000–20099')!
    expect(nat.protocols[0].natTarget).toBe(443)
  })
})

describe('buildPortFocus', () => {
  it('includes only the reachable branch for a port', () => {
    const g = buildPortFocus(topology, 443)
    expect(g.nodes.length).toBe(5) // port + 2 proto + 2 svc
    expect(g.nodes.some((n) => n.type === 'port' && n.portStart === 443)).toBe(true)
    // Unrelated ports must not leak in.
    expect(g.nodes.some((n) => n.portStart === 8554)).toBe(false)
    expect(g.nodes.some((n) => n.portStart === 20000)).toBe(false)
  })

  it('focus on a range port keeps its protocol', () => {
    const g = buildPortFocus(topology, 20000, 20099)
    expect(g.nodes.some((n) => n.type === 'protocol' && n.protocol === 'udp')).toBe(true)
  })
})

describe('buildServiceFocus', () => {
  it('finds upstream entrypoints reaching a service', () => {
    const g = buildServiceFocus(topology, 'hysteria2')
    // Hysteria2 is reached from port 443 (direct) and 20000-20099 (NAT).
    const ports = g.nodes.filter((n) => n.type === 'port').map((n) => n.portStart)
    expect(ports).toContain(443)
    expect(ports).toContain(20000)
  })

  it('includes downstream dependencies', () => {
    const g = buildServiceFocus(topology, 'shadowtls')
    const svc = g.nodes.filter((n) => n.type === 'service').map((n) => n.serviceId)
    expect(svc).toContain('snell')
  })
})
