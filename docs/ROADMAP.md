# Roadmap

## v0.1 — Walking skeleton (CLI) ✅ 2026-11-14
- [x] `config` : chargement YAML, `${ENV}` / `${file:...}`, defaults, validation (ids uniques, notifiers référencés existants)
- [x] `fetch` : client HTTP (proxy entreprise, CA, timeout, deny_cidrs, ETag)
- [x] `fetch` : authentification `header` | `query` | `basic` | `bearer` | `oauth2_client_credentials` | `mtls`, credentials limités à l'origine configurée
- [x] `normalize` : OAS 3.x + Swagger 2.0 → forme canonique + hash
- [x] `diff` : wrapper oasdiff → changements classés + `in_scope`
- [x] `store` : migrations, blobs content-addressed, sync YAML → `apis`, snapshots/changes
- [x] CLI `kansabooj validate`, `kansabooj check <id>`
- [x] `config/apis.schema.json` (JSON Schema pour l'autocomplétion IDE)

## v0.2 — Serveur, front & notifications
- [ ] `scheduler` : interval + jitter, check manuel, statuts `error` / `auth_error` / `stale`
- [ ] API REST `/api/*`
- [ ] Front : liste, timeline, détail d'un changement, statut
- [ ] Notifiers `googlechat`, `slack`, `teams`, `generic` (HMAC) avec retry/backoff
- [ ] Acquittement des changements (header SSO)
- [ ] Notifications `stale` / `recovered`

## v0.3 — Robustesse
- [ ] `discover` : Swagger-UI (initializer, swagger-config, `urls:` multiples), Redoc, heuristiques de chemins
- [ ] Mode dégradé `html` (hash du texte extrait)
- [ ] Specs multi-fichiers (`$ref` externes)
- [ ] `/metrics` Prometheus, purge des `checks`
- [ ] Image Docker distroless, déploiement

## Plus tard (selon retours)
- `gateway.published_spec` : périmètre déduit du contrat publié + détection de drift amont ↔ publié (agnostique de la Gateway)
- Autres formats : AsyncAPI, GraphQL SDL, WSDL (interface `Differ` par format)
- Digest quotidien/hebdo au lieu de notifications unitaires
- UI admin qui ouvre une MR sur le dépôt de config (plutôt qu'écrire en base)
