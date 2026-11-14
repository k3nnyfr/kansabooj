import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { api, type Comparison, type DiffLine, type Snapshot } from '../api/client'
import { Counts, LevelBadge } from '../components/common'
import { useI18n } from '../i18n'

// Free comparison between two versions of an API: semantic changes (oasdiff) and a
// side-by-side view of the normalized documents.
export function Compare() {
  const { id = '' } = useParams()
  const { t } = useI18n()
  const [params, setParams] = useSearchParams()
  const detail = useQuery({ queryKey: ['api', id], queryFn: () => api.get(id) })
  const snaps = useQuery({ queryKey: ['snapshots', id], queryFn: () => api.snapshots(id) })

  const list = snaps.data ?? []
  // default: the previous version against the latest one
  const to = Number(params.get('to')) || list[0]?.id
  const from = Number(params.get('from')) || list[1]?.id
  const ready = !!from && !!to && from !== to
  const cmp = useQuery({ queryKey: ['compare', id, from, to], queryFn: () => api.compare(id, from!, to!), enabled: ready })

  const pick = (key: 'from' | 'to', v: number) => {
    const next = new URLSearchParams(params)
    next.set('from', String(key === 'from' ? v : from))
    next.set('to', String(key === 'to' ? v : to))
    setParams(next, { replace: true })
  }

  return (
    <>
      <Link to={`/apis/${encodeURIComponent(id)}`} className="back">← {detail.data?.api.name ?? id}</Link>
      <h1>{t('compare.title')}</h1>
      {snaps.isLoading && <p className="muted">{t('loading')}</p>}
      {snaps.error && <p className="callout callout-error">{t('error.load', { msg: snaps.error.message })}</p>}
      {snaps.data && list.length < 2 && <p className="muted">{t('compare.onlyOne')}</p>}
      {list.length >= 2 && (
        <div className="compare-pick">
          <VersionSelect label={t('compare.from')} value={from} list={list} onChange={(v) => pick('from', v)} />
          <button className="btn btn-ghost btn-small" onClick={() => setParams({ from: String(to), to: String(from) }, { replace: true })}>
            ⇄ {t('compare.swap')}
          </button>
          <VersionSelect label={t('compare.to')} value={to} list={list} onChange={(v) => pick('to', v)} />
        </div>
      )}
      {list.length >= 2 && !ready && <p className="muted">{t('compare.same')}</p>}
      {cmp.isLoading && <p className="muted">{t('loading')}</p>}
      {cmp.error && <p className="callout callout-error">{t('error.load', { msg: cmp.error.message })}</p>}
      {cmp.data && <Result c={cmp.data} fromID={from!} toID={to!} />}
    </>
  )
}

function VersionSelect({ label, value, list, onChange }: { label: string; value?: number; list: Snapshot[]; onChange: (v: number) => void }) {
  const { t, date } = useI18n()
  return (
    <label className="small">
      {label}{' '}
      <select value={value} onChange={(e) => onChange(Number(e.target.value))}>
        {list.map((s) => (
          <option key={s.id} value={s.id}>
            {t('compare.option', { date: date(s.fetched_at), v: s.version || 'n/a' })}
          </option>
        ))}
      </select>
    </label>
  )
}

function Result({ c, fromID, toID }: { c: Comparison; fromID: number; toID: number }) {
  const { t, lang } = useI18n()
  const [showOut, setShowOut] = useState(false)
  const items = c.summary.items
  const outScope = items.filter((i) => !i.in_scope).length
  const shown = showOut ? items : items.filter((i) => i.in_scope)
  const td = c.text_diff

  return (
    <>
      <section>
        <h2>
          {t('compare.changes')} {items.length > 0 && <Counts errors={c.summary.errors} warns={c.summary.warns} infos={c.summary.infos} />}
        </h2>
        {items.length === 0 ? (
          <p className="muted">{t('compare.noChange')}</p>
        ) : (
          <>
            <ul className="items compare-items">
              {shown.map((it, i) => (
                <li key={i} className={it.in_scope ? '' : 'out'}>
                  <LevelBadge level={it.level} />
                  {(it.method || it.path) && <span className="mono op">{it.method} {it.path}</span>}
                  <span className="text">{(lang === 'fr' && it.text_fr) || it.text}</span>
                  {!it.in_scope && <span className="small muted"> ({t('level.outOfScope')})</span>}
                </li>
              ))}
            </ul>
            <p className="small muted">
              {outScope > 0 && (
                <button className="link" onClick={() => setShowOut(!showOut)}>
                  {showOut ? t('action.hideOut') : t('action.showOut', { n: outScope })}
                </button>
              )}{' '}
              {t('compare.scopeNote')}
            </p>
          </>
        )}
      </section>

      <section>
        <h2>
          {t('compare.document')} <span className="small muted">{t('compare.lines', { added: td.added, removed: td.removed })}</span>
        </h2>
        <p className="small">
          <a href={api.specUrl(fromID, 'normalized')} target="_blank" rel="noreferrer">{t('compare.from')} #{fromID}</a>
          {' · '}
          <a href={api.specUrl(toID, 'normalized')} target="_blank" rel="noreferrer">{t('compare.to')} #{toID}</a>
        </p>
        {td.too_large && <p className="callout callout-warn small">{t('compare.tooLarge')}</p>}
        {td.truncated && <p className="callout callout-warn small">{t('compare.truncated')}</p>}
        {td.hunks.map((h, i) => (
          <SideBySide key={i} lines={h.lines} oldStart={h.old_start} newStart={h.new_start} />
        ))}
      </section>
    </>
  )
}

type Row = { left?: DiffLine; right?: DiffLine }

// Pairs removed and added lines of a same change block, so that a modified line shows
// its old and new text on the same row.
function rows(lines: DiffLine[]): Row[] {
  const out: Row[] = []
  for (let i = 0; i < lines.length; ) {
    if (lines[i].op === '=') {
      out.push({ left: lines[i], right: lines[i] })
      i++
      continue
    }
    const del: DiffLine[] = []
    const add: DiffLine[] = []
    for (; i < lines.length && lines[i].op !== '='; i++) (lines[i].op === '-' ? del : add).push(lines[i])
    for (let j = 0; j < Math.max(del.length, add.length); j++) out.push({ left: del[j], right: add[j] })
  }
  return out
}

function SideBySide({ lines, oldStart, newStart }: { lines: DiffLine[]; oldStart: number; newStart: number }) {
  const { t } = useI18n()
  return (
    <div className="sbs">
      <div className="sbs-hunk small muted">{t('compare.hunk', { old: oldStart, new: newStart })}</div>
      <table>
        <tbody>
          {rows(lines).map((r, i) => (
            <tr key={i}>
              <td className="ln">{r.left?.old ?? ''}</td>
              <td className={`code ${r.left ? (r.left.op === '-' ? 'del' : '') : 'empty'}`}>{r.left?.text}</td>
              <td className="ln">{r.right?.new ?? ''}</td>
              <td className={`code ${r.right ? (r.right.op === '+' ? 'add' : '') : 'empty'}`}>{r.right?.text}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
