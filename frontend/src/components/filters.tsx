import { useEffect, useId, useRef, useState } from 'react'
import { useI18n } from '../i18n'

export type FacetOption = { value: string; label: string; count: number }

/** Multi-select dropdown used identically for groups and tags: checkboxes with the number
 * of matching APIs, and a filter field when the list is long. */
export function FacetFilter({ label, options, selected, onChange }: {
  label: string
  options: FacetOption[]
  selected: string[]
  onChange: (values: string[]) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const panelId = useId()

  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent | KeyboardEvent) => {
      if (e instanceof KeyboardEvent ? e.key === 'Escape' : !root.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', close)
    }
  }, [open])

  const toggle = (v: string) => onChange(selected.includes(v) ? selected.filter((x) => x !== v) : [...selected, v])
  const needle = q.trim().toLowerCase()
  const shown = options.filter((o) => !needle || o.label.toLowerCase().includes(needle))

  return (
    <div className="facet" ref={root}>
      <button type="button" className={`select facet-btn ${selected.length ? 'active' : ''}`}
        aria-expanded={open} aria-controls={panelId} onClick={() => setOpen(!open)}>
        {label}
        {selected.length > 0 && <span className="facet-n">{selected.length}</span>}
        <span aria-hidden className="caret">▾</span>
      </button>
      {open && (
        <div className="facet-panel" id={panelId} role="group" aria-label={label}>
          {options.length > 8 && (
            <input className="search facet-search" type="search" autoFocus placeholder={t('list.facet.filter')}
              value={q} onChange={(e) => setQ(e.target.value)} aria-label={t('list.facet.filter')} />
          )}
          <ul>
            {shown.map((o) => (
              <li key={o.value}>
                <label className={o.count === 0 && !selected.includes(o.value) ? 'muted' : ''}>
                  <input type="checkbox" checked={selected.includes(o.value)} onChange={() => toggle(o.value)} />
                  <span className="facet-label">{o.label}</span>
                  <span className="chip-count">{o.count}</span>
                </label>
              </li>
            ))}
            {shown.length === 0 && <li className="muted small">{t('list.facet.empty')}</li>}
          </ul>
        </div>
      )}
    </div>
  )
}

/** Two-position switch ("Alphabetical / Groups"). */
export function Segmented<T extends string>({ label, value, options, onChange }: {
  label: string
  value: T
  options: { value: T; label: string }[]
  onChange: (v: T) => void
}) {
  return (
    <div className="segmented" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" role="radio" aria-checked={value === o.value}
          className={value === o.value ? 'on' : ''} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

/** Checkbox supporting the "some selected" state (header and group rows). */
export function TriCheckbox({ checked, indeterminate, onChange, label }: {
  checked: boolean
  indeterminate: boolean
  onChange: (checked: boolean) => void
  label: string
}) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate
  }, [indeterminate])
  return <input ref={ref} type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} aria-label={label} />
}
