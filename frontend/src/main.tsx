import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { StrictMode, useEffect, useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import { api } from './api/client'
import { I18nProvider, useI18n } from './i18n'
import { ApiDetail } from './pages/ApiDetail'
import { Compare } from './pages/Compare'
import { ApiList } from './pages/ApiList'
import { StatusPage } from './pages/StatusPage'
import './styles.css'

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1, staleTime: 10_000 } } })

function Shell() {
  const { t, lang, setLang } = useI18n()
  const meta = useQuery({ queryKey: ['meta'], queryFn: api.meta, staleTime: Infinity })
  // Sticky table headers stop right below the top bar, whose height depends on wrapping.
  const bar = useRef<HTMLElement>(null)
  useEffect(() => {
    const el = bar.current
    if (!el) return
    const ro = new ResizeObserver(() => document.documentElement.style.setProperty('--topbar-h', `${el.offsetHeight}px`))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return (
    <>
      <header className="topbar" ref={bar}>
        <NavLink to="/" className="brand" end>
          <svg viewBox="0 0 32 32" width="22" height="22" aria-hidden>
            <rect width="32" height="32" rx="7" fill="var(--accent)" />
            <path d="M7 20h4l3-9 4 13 3-7h4" stroke="white" strokeWidth="2.5" fill="none" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
          Kansabooj
        </NavLink>
        <nav>
          <NavLink to="/" end>{t('nav.apis')}</NavLink>
          <NavLink to="/status">{t('nav.status')}</NavLink>
        </nav>
        <span className="spacer" />
        {meta.data && meta.data.user !== 'anonymous' && <span className="small muted">{meta.data.user}</span>}
        <button className="btn btn-ghost" onClick={() => setLang(lang === 'fr' ? 'en' : 'fr')} lang={lang === 'fr' ? 'en' : 'fr'}>
          {t('lang.switch')}
        </button>
      </header>
      <main className="container">
        <Routes>
          <Route path="/" element={<ApiList />} />
          <Route path="/apis/:id" element={<ApiDetail />} />
          <Route path="/apis/:id/compare" element={<Compare />} />
          <Route path="/status" element={<StatusPage />} />
        </Routes>
      </main>
      {meta.data && <footer className="footer small muted">Kansabooj {meta.data.version}</footer>}
    </>
  )
}

function App() {
  const meta = useQuery({ queryKey: ['meta'], queryFn: api.meta, staleTime: Infinity })
  return (
    <I18nProvider serverDefault={meta.data?.language}>
      <BrowserRouter>
        <Shell />
      </BrowserRouter>
    </I18nProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
