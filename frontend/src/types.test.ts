import { describe, it, expect } from 'vitest'
import { buildPortTree, sortServices, formatBytes, formatDuration } from '@/types'
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
