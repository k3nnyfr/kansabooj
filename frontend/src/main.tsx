import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { StrictMode, useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import { api } from './api/client'
import { I18nProvider, useI18n } from './i18n'
import { ApiDetail } from './pages/ApiDetail'
import { Compare } from './pages/Compare'
import { ApiList } from './pages/ApiList'
import { Journal } from './pages/Journal'
import { StatusPage } from './pages/StatusPage'
import './styles.css'

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1, staleTime: 10_000 } } })

type Theme = 'light' | 'dark'
const THEME_KEY = 'kansabooj.theme'

// Light / dark theme: index.html sets data-theme before the first render (stored choice, else the
// system's). The system is followed until the user picks a theme, which is then remembered.
function useTheme(): [Theme, () => void] {
  const root = document.documentElement
  const [theme, setTheme] = useState<Theme>(root.dataset.theme === 'dark' ? 'dark' : 'light')
  const apply = (t: Theme) => {
    root.dataset.theme = t
    setTheme(t)
  }
  useEffect(() => {
    const mq = matchMedia('(prefers-color-scheme: dark)')
    const follow = () => {
      let stored: string | null = null
      try {
        stored = localStorage.getItem(THEME_KEY)
      } catch {
        /* storage unavailable: follow the system */
      }
      if (!stored) apply(mq.matches ? 'dark' : 'light')
    }
    mq.addEventListener('change', follow)
    return () => mq.removeEventListener('change', follow)
  }, [])
  const toggle = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark'
    try {
      localStorage.setItem(THEME_KEY, next)
    } catch {
      /* not remembered, still applied */
    }
    apply(next)
  }
  return [theme, toggle]
}

function ThemeIcon({ theme }: { theme: Theme }) {
  // The icon shows the theme a click switches to.
  return theme === 'dark' ? (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
      <circle cx="12" cy="12" r="4.5" />
      <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
    </svg>
  ) : (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a8.5 8.5 0 1 0 11 11z" />
    </svg>
  )
}

function Shell() {
  const { t, lang, setLang } = useI18n()
  const [theme, toggleTheme] = useTheme()
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
          <span className="brand-name">Kansabooj</span>
        </NavLink>
        <nav>
          <NavLink to="/" end>{t('nav.apis')}</NavLink>
          <NavLink to="/journal">{t('nav.journal')}</NavLink>
          <NavLink to="/status">{t('nav.status')}</NavLink>
        </nav>
        <span className="spacer" />
        {meta.data && meta.data.user !== 'anonymous' && <span className="small muted topbar-user">{meta.data.user}</span>}
        <button className="btn btn-ghost" onClick={() => setLang(lang === 'fr' ? 'en' : 'fr')} lang={lang === 'fr' ? 'en' : 'fr'}
          aria-label={t('lang.switch')}>
          <span className="wide-only">{t('lang.switch')}</span>
          <span className="narrow-only" aria-hidden>{lang === 'fr' ? 'EN' : 'FR'}</span>
        </button>
        <button className="btn btn-ghost btn-icon" onClick={toggleTheme}
          title={t(theme === 'dark' ? 'theme.toLight' : 'theme.toDark')} aria-label={t(theme === 'dark' ? 'theme.toLight' : 'theme.toDark')}>
          <ThemeIcon theme={theme} />
        </button>
      </header>
      <main className="container">
        <Routes>
          <Route path="/" element={<ApiList />} />
          <Route path="/apis/:id" element={<ApiDetail />} />
          <Route path="/apis/:id/compare" element={<Compare />} />
          <Route path="/journal" element={<Journal />} />
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
