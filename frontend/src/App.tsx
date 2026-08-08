import { useCallback, useEffect, useMemo, useState } from 'react'
import type { State } from '@/types'
import { createPollController, restartService } from '@/api'
import { HostSummary } from '@/components/HostSummary'
import { TopologyView } from '@/components/TopologyView'
import { ServiceList } from '@/components/ServiceList'
import { DetailDrawer } from '@/components/DetailDrawer'
import { MobileTopology, BottomSheet } from '@/components/MobileTopology'
import { COLORS } from '@/theme'

export default function App() {
  const [state, setState] = useState<State | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < 768)

  const onData = useCallback((s: State) => {
    setState(s)
    setError(null)
  }, [])
  const onError = useCallback((e: unknown) => {
    setError(e instanceof Error ? e.message : 'load failed')
  }, [])

  useEffect(() => {
    const controller = createPollController(onData, onError)
    const stop = controller.subscribe()
    return stop
  }, [onData, onError])

  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth < 768)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const selectedService = useMemo(() => {
    if (!state || !selectedId) return null
    return state.services.find((s) => s.id === selectedId) ?? null
  }, [state, selectedId])

  const handleRestart = useCallback(async (serviceId: string) => {
    await restartService(serviceId)
  }, [])

  return (
    <div className="oc-app">
      {!state && !error && <div className="oc-loading">Loading…</div>}
      {!state && error && (
        <div className="oc-loading oc-error">
          Cannot reach the server: {error}
          <div className="oc-dim">Run <code>opscockpit collect</code> then <code>opscockpit serve</code>.</div>
        </div>
      )}

      {state && isMobile && (
        <div className="oc-mobile">
          <div className="oc-mobile-head">
            <span className="oc-brand">OpsCockpit</span>
            <span
              className="oc-statusbadge"
              style={{
                color: COLORS.green,
                borderColor: `${COLORS.green}55`,
                background: `${COLORS.green}14`,
              }}
            >
              {state.health.status}
            </span>
          </div>
          <HostSummary state={state} />
          <MobileTopology state={state} selectedId={selectedId ?? undefined} onSelectService={setSelectedId} />
          <BottomSheet service={selectedService} onClose={() => setSelectedId(null)} onRestart={handleRestart} />
        </div>
      )}

      {state && !isMobile && (
        <div className="oc-desktop">
          <header className="oc-header">
            <span className="oc-brand">OpsCockpit</span>
            <span className="oc-dim oc-header-sub">{state.host.hostname}</span>
            <span className="oc-header-spacer" />
            <span className="oc-dim">state {state.health.age_seconds}s old</span>
          </header>

          <div className="oc-main">
            <div className="oc-col oc-col-left">
              <HostSummary state={state} />
              <div className="oc-topology-wrap">
                <TopologyView state={state} onSelectService={setSelectedId} />
              </div>
            </div>
            <div className="oc-col-right">
              <ServiceList services={state.services} selectedId={selectedId ?? undefined} onSelect={setSelectedId} />
              <DetailDrawer service={selectedService} onClose={() => setSelectedId(null)} onRestart={handleRestart} />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
