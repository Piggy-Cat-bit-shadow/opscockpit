import type { BoardCard, BoardProtocol } from '@/topology'

interface Props {
  cards: BoardCard[]
  onOpenPort: (start: number, end: number) => void
}

function ProtocolRow({ p }: { p: BoardProtocol }) {
  return (
    <div className="oc-card-proto">
      <span className={`oc-card-proto-badge oc-card-proto-${p.protocol}`}>{p.protocol.toUpperCase()}</span>
      <span className="oc-card-proto-services">
        {p.natTarget != null && <span className="oc-card-nat">NAT → {p.natTarget}</span>}
        {p.services.join(' → ')}
      </span>
    </div>
  )
}

export function IngressBoard({ cards, onOpenPort }: Props) {
  if (cards.length === 0) {
    return <div className="oc-board-empty">No public ingress discovered.</div>
  }
  return (
    <div className="oc-board">
      {cards.map((c: BoardCard) => (
        <button key={c.label} className="oc-card" onClick={() => onOpenPort(c.portStart, c.portEnd)}>
          <div className="oc-card-head">
            <span className="oc-card-port">{c.label}</span>
            {c.exposure && <span className="oc-card-exposure">{c.exposure === 'nat_ingress' ? 'NAT ingress' : c.exposure}</span>}
          </div>
          <div className="oc-card-protos">
            {c.protocols.map((p) => (
              <ProtocolRow key={p.protocol} p={p} />
            ))}
          </div>
        </button>
      ))}
    </div>
  )
}
