import { useIsMutating, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type ApiStatus, type CheckResult, type Level } from '../api/client'
import { useI18n } from '../i18n'

export function StatusBadge({ status }: { status: ApiStatus }) {
  const { t } = useI18n()
  return <span className={`badge status-${status}`}>{t(`status.${status}`)}</span>
}

export function TagList({ tags, onClick }: { tags: string[]; onClick?: (tag: string) => void }) {
  if (!tags.length) return null
  return (
    <span className="tags">
      {tags.map((x) =>
        onClick ? (
          <button key={x} className="tag" onClick={() => onClick(x)}>#{x}</button>
        ) : (
          <span key={x} className="tag">#{x}</span>
        ),
      )}
    </span>
  )
}

export function LevelBadge({ level }: { level: Level }) {
  const { t } = useI18n()
  return <span className={`badge level-${level}`}>{t(`level.${level}`)}</span>
}

export function Counts({ errors, warns, infos }: { errors: number; warns: number; infos: number }) {
  const { t } = useI18n()
  return (
    <span className="counts">
      {errors > 0 && <span className="badge level-error">{errors} {t('level.error')}</span>}
      {warns > 0 && <span className="badge level-warn">{warns} {t('level.warn')}</span>}
      {infos > 0 && <span className="badge level-info">{infos} {t('level.info')}</span>}
    </span>
  )
}

const knownKinds = ['auth', 'network', 'http', 'denied', 'too_large', 'invalid_spec', 'unsupported', 'config']

export function ErrorHint({ kind, detail, consecutive }: { kind?: string; detail?: string; consecutive?: number }) {
  const { t } = useI18n()
  const key = (kind && knownKinds.includes(kind) ? `errkind.${kind}` : 'errkind.other') as Parameters<typeof t>[0]
  return (
    <div className="callout callout-error" role="alert">
      <strong>{t(key)}</strong>
      {detail && <div className="mono small">{detail}</div>}
      {!!consecutive && <div className="small muted">{t('errors.consecutive', { n: consecutive })}</div>}
    </div>
  )
}

/** Mutation key shared by every manual check, so that a row shows "Verifying…" whether the
 * check came from its own button or from a bulk action (variables = API id). */
export const checkKey = ['check']

/** Button triggering a manual check; invalidates cached data afterwards. Both labels are
 * laid out in the same grid cell, so the button keeps its width while the check runs. */
export function CheckButton({ id, onResult }: { id: string; onResult?: (r: CheckResult) => void }) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const running = useIsMutating({ mutationKey: checkKey, predicate: (m) => m.state.variables === id }) > 0
  const m = useMutation({
    mutationKey: checkKey,
    mutationFn: (apiID: string) => api.check(apiID),
    onSuccess: (r) => {
      onResult?.(r)
      qc.invalidateQueries()
    },
  })
  return (
    <button className="btn btn-stable" onClick={() => m.mutate(id)} disabled={running} aria-busy={running}>
      <span className={running ? 'off' : ''}>{t('action.check')}</span>
      <span className={running ? '' : 'off'}>{t('action.checking')}</span>
    </button>
  )
}
