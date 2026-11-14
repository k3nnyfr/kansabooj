import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api/client'
import { StatusBadge } from '../components/common'
import { useI18n } from '../i18n'

export function StatusPage() {
  const { t, relative, date } = useI18n()
  const { data, error, isLoading } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 15_000 })
  if (isLoading) return <p className="muted">{t('loading')}</p>
  if (error) return <p className="callout callout-error">{t('error.load', { msg: error.message })}</p>
  const s = data!
  const next = Object.entries(s.scheduler.next_checks).sort((a, b) => a[1].localeCompare(b[1]))

  return (
    <>
      <h1>{t('status.title')}</h1>
      <section className="card">
        <h2>{t('status.scheduler')}</h2>
        <dl className="kv">
          <dt>{t('status.lastTick')}</dt><dd title={date(s.scheduler.last_tick)}>{relative(s.scheduler.last_tick)}</dd>
          <dt>{t('status.configAt')}</dt><dd>{date(s.scheduler.config_loaded_at)}</dd>
          <dt>{t('status.running')}</dt><dd className="mono">{s.scheduler.running.join(', ') || t('status.none')}</dd>
        </dl>
      </section>

      <section className="card">
        <h2>{t('status.failing')} ({s.failing.length}/{s.total})</h2>
        {s.failing.length === 0 ? (
          <p className="muted">{t('status.allGood')}</p>
        ) : (
          <ul className="plain">
            {s.failing.map((a) => (
              <li key={a.id}>
                <Link to={`/apis/${a.id}`}>{a.name}</Link> <StatusBadge status={a.status} />
                <div className="small mono muted">{a.last_error}</div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="card">
        <h2>{t('status.next')}</h2>
        <ul className="plain small">
          {next.map(([id, at]) => (
            <li key={id}><Link to={`/apis/${id}`} className="mono">{id}</Link> <span className="muted" title={date(at)}>{relative(at)}</span></li>
          ))}
        </ul>
      </section>
    </>
  )
}
