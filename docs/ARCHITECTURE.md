# Kansabooj — Architecture & challenge de la solution

> Surveiller les contrats d'API de partenaires réexposées dans l'API Gateway interne,
> historiser leurs évolutions et notifier les équipes quand elles changent.

## 1. Le besoin, reformulé

| # | Besoin | Réponse |
|---|--------|---------|
| B1 | Renseigner des URLs de documentation d'API (OpenAPI 3.x, Swagger 2.0 JSON/YAML, Swagger-UI, Redoc…) | Fichier `config/apis.yml` + moteur de *discovery* qui retrouve le vrai fichier de spec derrière une page HTML |
| B2 | Vérifier périodiquement version + méthodes/paramètres | Scheduler interne, fetch → normalisation → hash → diff sémantique (oasdiff) |
| B3 | Historiser | Snapshots content-addressed (on ne stocke une spec que si elle change) + changelog par évolution |
| B4 | Front : liste + indicateur de mise à jour + timeline | SPA React servie par le binaire backend |
| B5 | Notifications webhooks (Google Chat, Slack, autre) | Notifiers typés (formats natifs) + générique JSON, filtrés par sévérité |

## 2. Challenge — ce qui va réellement poser problème

### 2.1 « Swagger-UI » n'est pas un contrat
Une page Swagger-UI / Redoc est du HTML qui *charge* une spec. Il faut la retrouver :
`swagger-initializer.js` (`url:` / `urls:`), `swagger-config` (`/v3/api-docs/swagger-config` côté springdoc),
attribut `spec-url` de Redoc, `<link rel="...">`, puis heuristiques classiques (`/v3/api-docs`, `/openapi.json`, `/swagger.json`…).
**Décision** : un module `discover` dédié, qui résout une URL en *URL de spec* et enregistre ce qu'il a trouvé.
Pour une doc purement HTML/PDF sans spec : **mode dégradé** = hash du texte extrait, détection « ça a changé » sans diff sémantique. On l'affiche clairement comme tel, on ne fait pas semblant.

### 2.2 Le bruit tuera l'outil avant les vrais changements
Si on compare des octets, on aura des faux positifs en permanence : ordre des clés, `servers` qui varient selon l'environnement,
timestamps injectés, exemples régénérés, whitespace, JSON ↔ YAML.
**Décision** : on compare une **forme normalisée** (parse → conversion Swagger 2.0 → OAS 3 → tri des clés → suppression d'un jeu de champs ignorables configurable → sérialisation canonique) et on hashe ça.
La spec brute est conservée à côté pour audit. Les règles d'ignore (`ignore:` en JSONPath) sont configurables par API.

### 2.3 `info.version` ment
Beaucoup de partenaires ne bumpent jamais la version, ou la bumpent sans rien changer. La version est un **signal secondaire** ;
le signal primaire est le diff du contrat. On remonte quand même « version changée sans changement de contrat » (niveau `info`).

### 2.4 Ce qui compte, ce n'est pas « l'API a changé » mais « ce que *nous* exposons est impacté »
C'est le point le plus important du challenge. Un partenaire avec 400 endpoints qui ajoute un champ dans un endpoint
que vous ne réexposez pas ne doit pas réveiller une équipe.
**Décision** : chaque API déclare (optionnellement) le sous-ensemble réexposé (`gateway.exposed_operations`).
Les changements sont classés **impactant / hors périmètre**, et seules les notifications « impactant » partent par défaut.
L'outil est **agnostique de la plateforme de Gateway** : aucun connecteur propriétaire.
**v2** (agnostique) : `gateway.published_spec` = URL ou fichier du contrat *publié* dans la Gateway (toutes les plateformes
savent exporter de l'OpenAPI) → le périmètre se déduit de ses opérations, et on détecte le *drift* amont ↔ publié.

### 2.5 Classer les changements, pas juste les lister
oasdiff classe chaque changement en `ERR` (breaking), `WARN` (potentiellement breaking), `INFO`.
Les notifiers ont un `min_level`. Sans ça, Slack/Chat deviennent un canal qu'on mute au bout de deux semaines.

### 2.6 L'outil fait lui-même des appels sortants
Ironie du besoin : un outil qui contrôle les flux sortants fait des flux sortants vers des dizaines de domaines.
- **Acté** : tout sort par le **proxy d'entreprise** (`http.proxy`, défaut `HTTPS_PROXY`), avec **CA interne** (le proxy fait
  probablement de l'inspection TLS), timeouts, User-Agent identifiable.
- Conséquence anti-SSRF : derrière un proxy, la résolution DNS est faite *par le proxy*. Le contrôle `deny_cidrs` reste
  appliqué localement (best effort : IP littérales + résolution locale du nom) mais la vraie barrière est la politique du proxy.
  Le principal garde-fou reste la revue du YAML (ADR 0001).
- **Acté** : une partie des APIs exige une authentification pour lire la doc. Modes supportés dès la v0.1 :
  `header` (clé d'API), `query` (clé en paramètre), `basic`, `bearer` (token statique), `oauth2_client_credentials`
  (token mis en cache jusqu'à expiration), `mtls` (certificat client).
  - Secrets **jamais en clair dans le YAML** : `${ENV_VAR}` ou `${file:/run/secrets/x}`.
  - **Les credentials ne sont envoyés qu'à l'origine configurée** (schéma + hôte + port). Une redirection vers une autre
    origine, une spec découverte ou un `$ref` externe sur un autre domaine est fetché **sans** credentials.
  - Un `401/403` est un statut d'erreur dédié (`auth_error`) affiché comme tel dans le front : c'est le cas
    le plus fréquent de « l'outil ne marche plus » (clé expirée) et il doit sauter aux yeux.
- Politesse : `ETag` / `If-Modified-Since`, jitter sur les horaires, 1 fetch concurrent par domaine.

### 2.7 Erreur ≠ changement
Une API injoignable n'a pas « changé ». États distincts : `ok`, `changed`, `error`, `stale` (N erreurs consécutives).
On notifie une erreur seulement après N échecs consécutifs (configurable), et une seule fois jusqu'au retour à la normale.

### 2.8 Specs multi-fichiers
`$ref` externes (`./schemas/user.yaml`, URLs) → bundling avant normalisation (kin-openapi sait résoudre les refs externes).

### 2.9 YAML ou base de données pour la configuration ?

| | YAML (GitOps) | DB + UI admin |
|---|---|---|
| Traçabilité « qui a ajouté quoi » | git log + MR | à développer (audit log) |
| Revue avant ajout | MR | à développer (workflow) |
| Effort | faible | UI admin + authz + validation |
| Sécurité | URL relue avant fetch | **SSRF** : un utilisateur peut faire fetcher `http://169.254.169.254/` ou un service interne |
| Ajout par un non-dev | moins confortable | confortable |

**Recommandation : YAML comme source de vérité, la base ne stocke que l'état et l'historique.**
Au démarrage (et sur `SIGHUP` / `POST /api/admin/reload`), le YAML est synchronisé en base : les APIs retirées sont
désactivées, **jamais supprimées** (on garde l'historique). Le front est alors quasi read-only, ce qui simplifie énormément
l'authentification (SSO via reverse proxy suffit). Si un jour une UI admin est nécessaire, elle pourra générer une MR
sur le dépôt de config plutôt que d'écrire en base — on garde la revue et l'anti-SSRF humain.
Dans tous les cas : liste d'interdiction réseau (`deny_cidrs` : RFC1918, link-local, loopback) appliquée au fetcher.

### 2.9 bis Utilisable par n'importe quelle équipe
Le YAML ne doit pas être un frein. Pour qu'une équipe non spécialiste puisse ajouter une API sans aide :
- **JSON Schema** de `apis.yml` publié (`config/apis.schema.json`) → autocomplétion et validation dans VS Code / IntelliJ ;
- `kansabooj validate` : valide la config (schéma, ids uniques, notifiers existants, variables d'env présentes) — à brancher en CI sur le dépôt de config ;
- `kansabooj check <id>` : exécute un check à blanc et affiche ce qui a été trouvé (URL de spec découverte, format, version,
  nombre d'opérations, erreur d'auth explicite) **avant** de merger ;
- messages d'erreur en français et actionnables dans le front (« clé d'API refusée (401) — vérifier `PARTNER_X_DOC_KEY` »).

### 2.10 Build vs buy — à assumer explicitement
- **Bump.sh / APIMatic / Optic** font de la surveillance de changements d'API, mais en SaaS (les contrats partenaires sortent de l'entreprise) ou orienté « mes propres APIs ».
- **Le MVP minimal** serait un dépôt git + un pipeline CI planifié qui fait `curl` + `oasdiff changelog` + commit + webhook :
  l'historique git *est* la timeline. Ça couvre ~60 % du besoin en une journée.
  Ce qui justifie un outil dédié : la discovery Swagger-UI, la normalisation anti-bruit, le filtrage par périmètre exposé,
  l'indicateur « vu / pas vu » et une timeline lisible par des non-devs.
  → Si le budget est serré, commencer par le pipeline CI est un choix défendable.

### 2.11 Scheduling et haute dispo
Une instance unique avec scheduler interne suffit largement (quelques centaines d'APIs, check toutes les heures = négligeable).
Pas de Kafka, pas de Redis, pas de worker séparé. Si HA requise plus tard : verrou en base (advisory lock Postgres) pour élire le scheduler.

## 3. Choix techniques

| Sujet | Choix | Pourquoi |
|---|---|---|
| Backend | **Go** | oasdiff et kin-openapi sont des **bibliothèques Go** : diff sémantique, classification breaking, conversion Swagger 2 → OAS 3, résolution de `$ref`. En Python/Node il faudrait shell-out vers le binaire oasdiff. Binaire unique, facile à déployer. |
| Diff | `github.com/oasdiff/oasdiff` | Référence du marché, ~300 règles de détection, niveaux ERR/WARN/INFO |
| Parsing | `github.com/getkin/kin-openapi` | OAS 3.0/3.1 + `openapi2conv` |
| Stockage | **SQLite** (pure Go `modernc.org/sqlite`) | ~20-100 APIs, une instance, zéro ops. Migrations SQL versionnées. Postgres seulement si HA requise. |
| Front | **React + Vite + TypeScript**, TanStack Query, embarqué via `go:embed` | Un seul artefact à déployer |
| Scheduler | interne (`time.Ticker` + jitter, ou `robfig/cron`) | pas besoin d'infra |
| Notifs | notifiers typés : `slack` (Block Kit), `googlechat` (cardsV2), `teams`, `generic` (JSON signé HMAC) | les formats diffèrent vraiment entre plateformes |
| Observabilité | `/healthz`, `/metrics` Prometheus, logs JSON (`log/slog`) | |
| Auth front | reverse proxy SSO (header `X-Forwarded-User`) | le front est read-only sauf l'acquittement |

## 4. Flux d'un check

```
scheduler ──► fetch (discover si besoin, proxy, auth, ETag)
                │ 304 / même hash brut ─────────────► check(status=unchanged)
                ▼
            parse + bundle $ref + convert v2→v3
                ▼
            normalize (tri, ignore rules) ──► sha256(normalized)
                │ même hash normalisé ──────────────► check(status=unchanged)
                ▼
            store blob (content-addressed) + snapshot
                ▼
            oasdiff(previous, current) ──► change + change_items (level, op, path, in_scope)
                ▼
            notify (filtré par min_level + in_scope) ──► notifications (retry/backoff)
```

## 5. Modèle de données

Voir `backend/internal/store/migrations/0001_init.sql`. En résumé :

- `apis` — miroir du YAML (+ `enabled`, `resolved_spec_url`, état courant)
- `blobs` — specs brutes et normalisées, content-addressed (sha256, compressées zstd/gzip)
- `snapshots` — une ligne **par version distincte** d'une API (pas par check)
- `checks` — journal léger de chaque exécution (statut, durée, http, erreur) — purgé après N jours
- `changes` — un passage snapshot N-1 → N, compteurs par niveau, version from/to, acquittement
- `change_items` — chaque changement unitaire oasdiff (id de règle, niveau, méthode, path, texte, in_scope)
- `notifications` — envoi par notifier, statut, tentatives

## 6. API REST interne (consommée par le front)

| Méthode | Route | Rôle |
|---|---|---|
| GET | `/api/apis` | liste + statut + dernier changement + `unacknowledged_changes` |
| GET | `/api/apis/{id}` | détail, source résolue, dernier check |
| GET | `/api/apis/{id}/timeline` | changes paginés (la timeline) |
| GET | `/api/changes/{id}` | détail d'un changement + items |
| GET | `/api/snapshots/{id}/spec?form=raw\|normalized` | télécharger une version |
| GET | `/api/apis/{id}/diff?from=&to=` | diff entre deux snapshots arbitraires |
| POST | `/api/apis/{id}/check` | check manuel |
| POST | `/api/changes/{id}/ack` | acquitter (utilisateur = header SSO) |
| POST | `/api/admin/reload` | recharger le YAML |
| GET | `/healthz`, `/metrics` | ops |

## 7. Front

- **Liste** : nom, propriétaire, version courante, dernier check, badge (`à jour` / `N changements non vus` / `breaking` / `erreur`), filtre par équipe/statut.
- **Timeline** d'une API : une carte par changement (date, version from → to, compteurs ERR/WARN/INFO, hors-périmètre grisé), dépliable en liste de changements, lien « voir le diff brut » (vue split des specs normalisées).
- **Diff libre** entre deux versions choisies.
- **Statut** : APIs en erreur / stale, dernière exécution du scheduler.

## 8. Décisions actées (2026-11-14)

- Outil **agnostique de la plateforme de Gateway** (besoin global) — pas de connecteur propriétaire.
- Appels sortants via le **proxy d'entreprise**.
- Volumétrie : **une vingtaine d'APIs et plus** → SQLite confirmé, instance unique.
- **Authentification** sur une partie des sources → dans le périmètre v0.1 (§2.6).
- **Go** validé ; l'outil doit être utilisable par n'importe quelle équipe (§2.9 bis).

## 9. Questions ouvertes

1. Qui acquitte un changement, et l'acquittement doit-il être tracé nominativement ? (v0.2)
2. Besoin de surveiller aussi des APIs non-OpenAPI (GraphQL SDL, AsyncAPI, WSDL) ? → architecture prévue pour (interface `Differ` par format), pas implémenté en v1.
