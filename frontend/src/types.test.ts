import { describe, it, expect } from 'vitest'
import { buildPortTree, sortServices, formatBytes, formatDuration, formatPortRange } from '@/types'
import type { Service, Status, Topology } from '@/types'

function svc(id: string, name: string, status: Status, ram?: number): Service {
  return {
    id,
    name,
    status,
    memory: ram != null ? { rss_bytes: ram, source: 'proc_rss' } : undefined,
  }
}

describe('sortServices', () => {
  it('orders failed > warning > unknown > healthy, RAM desc within status', () => {
    const services = [
      svc('a', 'Healthy small', 'healthy', 10),
      svc('b', 'Healthy big', 'healthy', 100),
      svc('c', 'Failed', 'failed', 50),
      svc('d', 'Warning', 'warning', 30),
      svc('e', 'Unknown', 'unknown', 20),
    ]
    const sorted = sortServices(services)
    expect(sorted.map((s) => s.id)).toEqual(['c', 'd', 'e', 'b', 'a'])
  })
})

describe('buildPortTree', () => {
  const topology: Topology = {
    nodes: [
      { id: 'internet', type: 'internet', label: 'Internet' },
      { id: 'port-443', type: 'port', label: '443', port: 443 },
      { id: 'port-443-tcp', type: 'protocol', label: 'TCP', protocol: 'tcp', port: 443 },
      { id: 'nginx@tcp:443', type: 'service', label: 'Nginx', service_id: 'nginx', status: 'healthy', protocol: 'tcp', port: 443 },
      { id: 'port-443-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 443 },
      { id: 'hysteria2@udp:443', type: 'service', label: 'Hysteria2', service_id: 'hysteria2', status: 'healthy', protocol: 'udp', port: 443 },
      { id: 'port-8443-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 8443 },
      { id: 'tuic@udp:8443', type: 'service', label: 'TUIC', service_id: 'tuic', status: 'unknown', protocol: 'udp', port: 8443 },
    ],
    edges: [],
  }

  it('groups by port, then protocol, in ascending port order', () => {
    const tree = buildPortTree(topology)
    expect(tree.map((p) => p.port)).toEqual([443, 8443])
    const p443 = tree[0]
    expect(p443.protocols.map((p) => p.protocol)).toEqual(['tcp', 'udp'])
    expect(p443.protocols[0].services[0].name).toBe('Nginx')
    expect(p443.protocols[1].services[0].name).toBe('Hysteria2')
  })
})

describe('formatBytes', () => {
  it('formats units', () => {
    expect(formatBytes(0)).toBe('0.00 B')
    expect(formatBytes(1024)).toBe('1.00 KB')
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.00 MB')
    expect(formatBytes(null as unknown as number)).toBe('—')
  })
})

describe('formatDuration', () => {
  it('formats durations', () => {
    expect(formatDuration(30)).toBe('0m 30s')
    expect(formatDuration(3661)).toBe('1h 1m')
    expect(formatDuration(90000)).toBe('1d 1h')
  })
})

describe('formatPortRange', () => {
  it('renders single vs range', () => {
    expect(formatPortRange(443, 443)).toBe('443')
    expect(formatPortRange(20000, 20099)).toBe('20000–20099')
  })
})

describe('buildPortTree with ranges', () => {
  const topology: Topology = {
    nodes: [
      { id: 'port-20000-20099', type: 'port', label: '20000–20099', port_start: 20000, port_end: 20099 },
      { id: 'port-20000-20099-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port_start: 20000, port_end: 20099 },
      { id: 'hysteria2@udp:443', type: 'service', label: 'Hysteria2', service_id: 'hysteria2', protocol: 'udp', port: 443, port_start: 20000, port_end: 20099, status: 'healthy' },
      { id: 'port-8554', type: 'port', label: '8554', port: 8554, port_start: 8554, port_end: 8554 },
      { id: 'port-8554-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 8554, port_start: 8554, port_end: 8554 },
      { id: 'snell@udp:17414', type: 'service', label: 'Snell', service_id: 'snell', protocol: 'udp', port: 17414, port_start: 8554, port_end: 8554, status: 'healthy' },
    ],
    edges: [],
  }

  it('keys on range and sorts ranges before singles by start', () => {
    const tree = buildPortTree(topology)
    // 8554 (start 8554) sorts before 20000-20099 (start 20000).
    expect(tree.map((p) => p.label)).toEqual(['8554', '20000–20099'])
    expect(tree[0].portStart).toBe(8554)
    expect(tree[0].portEnd).toBe(8554)
    expect(tree[1].portStart).toBe(20000)
    expect(tree[1].portEnd).toBe(20099)
    // The range service still exposes its backend port.
    expect(tree[1].protocols[0].services[0].serviceId).toBe('hysteria2')
  })
})
