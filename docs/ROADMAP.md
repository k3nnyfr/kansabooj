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

## v0.2 — Serveur, front & notifications ✅ 2026-11-14
- [x] Commande `serve` : scheduler + API REST + front embarqué, reload de la config (SIGHUP / endpoint admin)
- [x] `scheduler` : interval + jitter, check manuel, statuts `error` / `auth_error` / `stale`
- [x] API REST `/api/*`
- [x] Front : liste, timeline, détail d'un changement, statut
- [x] Notifiers `googlechat`, `slack`, `teams`, `generic` (HMAC) avec retry/backoff
- [x] Acquittement des changements (header SSO)
- [x] Notifications `stale` / `recovered`
- [x] Langue **français / anglais** : langue par défaut (`server.language`), sélecteur dans le front, langue par notifier

## v0.3 — Groupes, tags, APIs internes & Kubernetes ✅ 2026-11-14
- [x] **PostgreSQL** en production (SQLite conservé en local / CLI), suite de tests exécutable sur les deux (ADR 0004)
- [x] Multi-réplicas : leader élu par verrou PostgreSQL, verrou par API, enregistrement optimiste des versions
- [x] Conteneur sans état : config par variables d'environnement, `/readyz`, rechargement à chaud ConfigMap / Secrets
- [x] Image (Containerfile multi-stage, non-root, compatible UID arbitraire OpenShift), manifests kustomize
      Kubernetes (Ingress) / OpenShift (Route), exemple CloudNativePG, compose local
- [x] **Groupes d'API** (`groups:`) : regroupement (partenaire, domaine, interne/externe…) portant des valeurs par défaut
      héritées par les APIs du groupe (owner, interval, notify, auth, profil réseau, ignore)
- [x] **Tags** libres par API (`tags: [paiement, pci, critique]`)
- [x] Front : filtres et vue regroupée par groupe / tag ; API REST filtrable (`?group=&tag=`)
- [x] Routage des notifications par groupe / tag (`notifiers[].subscribe`)
- [x] **Profils réseau** (`http.profiles`) pour les APIs internes : sans proxy, plages internes autorisées
      (`allow_cidrs`), CA dédiée — sans affaiblir le profil « internet » par défaut
- [x] JSON Schema + `validate` à jour (groupe inexistant, héritage)

## v0.3 bis — Mode API / as-code (ADR 0003)
- [ ] Configuration multi-fichiers `config/apis.d/*.yml` (un fichier par équipe / groupe)
- [ ] Spec OpenAPI de l'API Kansabooj servie sur `/api/openapi.yaml` (et Kansabooj surveillé par lui-même)
- [ ] `POST /api/config/validate` pour valider une MR de config depuis la CI
- [ ] Jetons d'API à portée limitée (`read`, `check`, `ack`, `admin`) pour les appels machine
- [ ] Guide « contrôle de contrat en CI » pour les équipes productrices internes (`check --dry-run`)

## v0.4 — Robustesse
- [ ] `discover` : Swagger-UI (initializer, swagger-config, `urls:` multiples), Redoc, heuristiques de chemins
- [ ] Mode dégradé `html` (hash du texte extrait)
- [ ] Specs multi-fichiers (`$ref` externes)
- [ ] `/metrics` Prometheus (purge des `checks` : fait en v0.2)
- [ ] Chart Helm (si les équipes d'exploitation le préfèrent à kustomize), NetworkPolicy d'egress
- [ ] CI : tests SQLite + PostgreSQL, build et scan de l'image

## Plus tard (selon retours)
- Traduction française des libellés de changements oasdiff (catalogue par `rule_id`, oasdiff n'existant qu'en anglais)
- CLI et messages d'erreur détaillés bilingues (aujourd'hui en français ; le front traduit déjà les catégories d'erreur)
- `gateway.published_spec` : périmètre déduit du contrat publié + détection de drift amont ↔ publié (agnostique de la Gateway)
- Autres formats : AsyncAPI, GraphQL SDL, WSDL (interface `Differ` par format)
- Digest quotidien/hebdo au lieu de notifications unitaires
- UI admin qui ouvre une MR sur le dépôt de config (plutôt qu'écrire en base)
