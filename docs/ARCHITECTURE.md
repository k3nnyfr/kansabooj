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
Une page Swagger-UI / Redoc est du HTML qui *charge* une spec. Module `discover` (v0.5), par ordre de confiance :
1. configuration explicite : paramètre `?url=`, `SwaggerUIBundle({ url | urls | configUrl })` dans la page ou dans les
   scripts **du même hôte** (`swagger-initializer.js`…), `swagger-config` de springdoc, `<redoc spec-url>`, `Redoc.init(…)`,
   variable JavaScript constante (`url: definitionURL` → `const definitionURL = "…"`) ;
2. URLs « ressemblant à une spec » mentionnées dans ces scripts, celles du même hôte d'abord (cas d'une URL calculée en JS) ;
3. emplacements usuels (`/openapi.json`, `/v3/api-docs`, `/swagger/v1/swagger.json`…), au plus 16 requêtes.

Chaque candidat est validé (c'est bien une spec OpenAPI/Swagger) avant d'être retenu. L'URL trouvée est mémorisée
(`resolved_spec_url`) et requêtée directement aux checks suivants, avec ETag ; si elle disparaît, la découverte est rejouée.
Plusieurs specs déclarées (`urls`) : la première par défaut, `source.spec_name` pour choisir.
Vérifié sur petstore3.swagger.io (URL calculée en JavaScript) et la démo Redoc.

**Mode dégradé `html`** : documentation sans spec exploitable. Le texte visible est extrait (scripts, styles, navigation
et pied de page exclus, `source.text_ignore` pour retirer dates et numéros de build), haché et comparé ligne à ligne :
une évolution est signalée en niveau `warn` (« vérification manuelle ») avec les lignes ajoutées/retirées, sans analyse
du contrat. Le type `auto` bascule en mode dégradé si aucune spec n'est trouvée, sans resonder ensuite les emplacements
usuels à chaque check ; si une spec apparaît plus tard derrière la page, le changement de mode est signalé.

**Specs multi-fichiers** : les `$ref` externes (OpenAPI 3) sont résolues via le client HTTP de l'API (politique réseau,
identifiants limités à l'origine), **http(s) uniquement** (jamais `file://` ni chemin local), au plus 64 fichiers, puis
internalisées : la forme canonique stockée est autonome.

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

### 2.9 ter Usage interne : groupes, tags et profils réseau
L'outil sert aussi à surveiller des **APIs internes**. Conséquences :
- **Conflit avec l'anti-SSRF** : les APIs internes vivent précisément dans les plages bloquées par `deny_cidrs`.
  On ne vide pas la liste : on introduit des **profils réseau** (`internet` par défaut : proxy + deny ; `internal` : sans proxy,
  `allow_cidrs` explicites, CA interne). Une API n'atteint le réseau interne que si elle (ou son groupe) le déclare.
- **Volume et organisation** : avec des dizaines d'APIs internes en plus, il faut des **groupes** (qui portent des valeurs par
  défaut : owner, notify, auth, profil réseau) et des **tags** libres pour filtrer le front et router les notifications.
- Valeur ajoutée côté interne : détecter qu'une équipe a cassé son contrat **avant** que ses consommateurs ne s'en aperçoivent.
  À terme, l'outil pourrait aussi être branché en CI des producteurs (`kansabooj check --dry-run` sur la spec du build).

### 2.9 quater Langue
Interface, notifications et messages disponibles en **français et anglais**, au choix : langue par défaut côté serveur
(`server.language`), sélecteur par utilisateur dans le front (mémorisé dans le navigateur), langue par notifier (un channel
anglophone et un francophone peuvent coexister). Limite assumée : les libellés détaillés des changements viennent d'oasdiff
et n'existent qu'en anglais ; on stocke le `rule_id` pour pouvoir les traduire plus tard.

### 2.10 Build vs buy — l'existant (revu le 2026-11-14)
- **Moteurs de diff open source** (briques, pas des produits) : **oasdiff** (Go, ~300 règles, utilisé ici comme bibliothèque),
  OpenAPITools/openapi-diff (Java), Azure/openapi-diff (npm), Criteo openapi-comparator. Aucun ne planifie, n'historise
  ni ne notifie : c'est à construire autour.
- **Services hébergés de surveillance de specs tierces** : SchemaWatch, oasdiff Sync (alertes Slack, des auteurs d'oasdiff),
  FlareCanary, Flowdev, des « actors » Apify payants à l'usage, et des moniteurs de pages génériques (PageCrawl) qui détectent
  tout changement d'octet sans comprendre le contrat. Tous en **SaaS** : la liste des partenaires et les contrats sortent de
  l'entreprise, pas d'auth sur réseau interne, pas de notion de périmètre réexposé dans une Gateway.
- **Plateformes de doc / gouvernance** (Bump.sh, APIMatic, Optic) : orientées « mes propres APIs ».
- **Le MVP minimal** reste un dépôt git + un pipeline CI planifié (`curl` + `oasdiff changelog` + commit + webhook) :
  l'historique git *est* la timeline. Ce qui justifie un outil dédié : auto-hébergé, specs authentifiées et internes,
  filtrage anti-bruit, filtrage par périmètre exposé, acquittement, timeline lisible par des non-devs.

### 2.10 bis Mode API / « as-code »
Voir **ADR 0003**. En bref : la configuration *est* déjà du code (YAML versionné) ; on la rend multi-équipes (`apis.d/`),
on documente l'API REST (spec OpenAPI de Kansabooj lui-même) et on ajoute une validation de config appelable depuis la CI.
On n'ouvre pas de CRUD d'APIs par HTTP (double source de vérité + SSRF) sans besoin concret.

### 2.11 Déploiement Kubernetes / OpenShift, scheduling et haute dispo
Cible principale : OpenShift / Kubernetes, avec **le moins d'adhérence possible aux fichiers** (ADR 0004).
- Conteneur **sans état** : état en PostgreSQL, configuration en ConfigMap, secrets en Secret (variables
  d'environnement ou fichiers `${file:}`), logs JSON sur stdout, racine en lecture seule, UID arbitraire (OpenShift).
- **Rechargement à chaud** : Kansabooj surveille l'empreinte du YAML *et* des fichiers secrets qu'il référence ;
  une mise à jour du ConfigMap ou une rotation de Secret est prise en compte sans redémarrage.
  Une configuration invalide est refusée et l'ancienne conservée.
- **Plusieurs réplicas** : un seul *leader* (verrou consultatif PostgreSQL) planifie les checks ; les autres servent
  l'interface et l'API. Bascule automatique si le leader disparaît (perte de session = perte du verrou).
- Sondes : `/healthz` (liveness, processus) et `/readyz` (readiness, base joignable).
- Manifests kustomize dans `deploy/kubernetes` (overlays `kubernetes` → Ingress, `openshift` → Route),
  exemple de base CloudNativePG, `deploy/compose.yaml` pour un essai local.

## 3. Choix techniques

| Sujet | Choix | Pourquoi |
|---|---|---|
| Backend | **Go** | oasdiff et kin-openapi sont des **bibliothèques Go** : diff sémantique, classification breaking, conversion Swagger 2 → OAS 3, résolution de `$ref`. En Python/Node il faudrait shell-out vers le binaire oasdiff. Binaire unique, facile à déployer. |
| Diff | `github.com/oasdiff/oasdiff` | Référence du marché, ~300 règles de détection, niveaux ERR/WARN/INFO |
| Parsing | `github.com/getkin/kin-openapi` | OAS 3.0/3.1 + `openapi2conv` |
| Stockage | **PostgreSQL** en production, **SQLite** en local/CLI (ADR 0004) | Déploiement Kubernetes/OpenShift sans état et multi-réplicas ; SQL portable, migrations par dialecte. |
| Front | **React + Vite + TypeScript**, TanStack Query, embarqué via `go:embed` | Un seul artefact à déployer |
| Scheduler | interne (`time.Ticker` + jitter), leader élu par verrou PostgreSQL | pas d'infra supplémentaire, HA native |
| Notifs | notifiers typés : `slack` (Block Kit), `googlechat` (cardsV2), `teams`, `generic` (JSON signé HMAC) | les formats diffèrent vraiment entre plateformes |
| Observabilité | `/healthz`, `/readyz`, `/metrics` Prometheus (état par API lu en base + compteurs d'instance), logs JSON (`log/slog`) | voir `docs/guides/exploitation.md` |
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

Voir `backend/internal/store/migrations/{postgres,sqlite}/`. En résumé :

- `apis` — miroir du YAML (+ `enabled`, `group_id`, `tags`, `network_profile`, `resolved_spec_url`, état courant)
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
- Volumétrie : **une vingtaine d'APIs et plus**, déploiement **Kubernetes / OpenShift** → PostgreSQL, multi-réplicas (ADR 0004, révisé le 2026-11-14).
- **Authentification** sur une partie des sources → dans le périmètre v0.1 (§2.6).
- **Go** validé ; l'outil doit être utilisable par n'importe quelle équipe (§2.9 bis).

## 9. Questions ouvertes

1. Qui acquitte un changement, et l'acquittement doit-il être tracé nominativement ? (v0.2)
2. Besoin de surveiller aussi des APIs non-OpenAPI (GraphQL SDL, AsyncAPI, WSDL) ? → architecture prévue pour (interface `Differ` par format), pas implémenté en v1.
