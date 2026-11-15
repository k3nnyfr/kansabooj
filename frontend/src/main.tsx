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

// Source repositories (icons from Simple Icons, CC0).
const REPOS = [
  {
    key: 'repo.gitea',
    href: 'https://git.k3nny.fr/k3nny/kansabooj',
    path: 'M4.209 4.603c-.247 0-.525.02-.84.088-.333.07-1.28.283-2.054 1.027C-.403 7.25.035 9.685.089 10.052c.065.446.263 1.687 1.21 2.768 1.749 2.141 5.513 2.092 5.513 2.092s.462 1.103 1.168 2.119c.955 1.263 1.936 2.248 2.89 2.367 2.406 0 7.212-.004 7.212-.004s.458.004 1.08-.394c.535-.324 1.013-.893 1.013-.893s.492-.527 1.18-1.73c.21-.37.385-.729.538-1.068 0 0 2.107-4.471 2.107-8.823-.042-1.318-.367-1.55-.443-1.627-.156-.156-.366-.153-.366-.153s-4.475.252-6.792.306c-.508.011-1.012.023-1.512.027v4.474l-.634-.301c0-1.39-.004-4.17-.004-4.17-1.107.016-3.405-.084-3.405-.084s-5.399-.27-5.987-.324c-.187-.011-.401-.032-.648-.032zm.354 1.832h.111s.271 2.269.6 3.597C5.549 11.147 6.22 13 6.22 13s-.996-.119-1.641-.348c-.99-.324-1.409-.714-1.409-.714s-.73-.511-1.096-1.52C1.444 8.73 2.021 7.7 2.021 7.7s.32-.859 1.47-1.145c.395-.106.863-.12 1.072-.12zm8.33 2.554c.26.003.509.127.509.127l.868.422-.529 1.075a.686.686 0 0 0-.614.359.685.685 0 0 0 .072.756l-.939 1.924a.69.69 0 0 0-.66.527.687.687 0 0 0 .347.763.686.686 0 0 0 .867-.206.688.688 0 0 0-.069-.882l.916-1.874a.667.667 0 0 0 .237-.02.657.657 0 0 0 .271-.137 8.826 8.826 0 0 1 1.016.512.761.761 0 0 1 .286.282c.073.21-.073.569-.073.569-.087.29-.702 1.55-.702 1.55a.692.692 0 0 0-.676.477.681.681 0 1 0 1.157-.252c.073-.141.141-.282.214-.431.19-.397.515-1.16.515-1.16.035-.066.218-.394.103-.814-.095-.435-.48-.638-.48-.638-.467-.301-1.116-.58-1.116-.58s0-.156-.042-.27a.688.688 0 0 0-.148-.241l.516-1.062 2.89 1.401s.48.218.583.619c.073.282-.019.534-.069.657-.24.587-2.1 4.317-2.1 4.317s-.232.554-.748.588a1.065 1.065 0 0 1-.393-.045l-.202-.08-4.31-2.1s-.417-.218-.49-.596c-.083-.31.104-.691.104-.691l2.073-4.272s.183-.37.466-.497a.855.855 0 0 1 .35-.077z',
  },
  {
    key: 'repo.github',
    href: 'https://github.com/k3nnyfr/kansabooj',
    path: 'M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12',
  },
] as const

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
        {REPOS.map((r) => (
          <a key={r.key} className="btn btn-ghost btn-icon wide-only" href={r.href} target="_blank" rel="noopener noreferrer"
            title={t(r.key)} aria-label={t(r.key)}>
            <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden><path d={r.path} /></svg>
          </a>
        ))}
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
