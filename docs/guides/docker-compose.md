# Déployer Kansabooj avec Docker Compose

Pour un serveur unique (VM, machine d'équipe) sans Kubernetes. Kubernetes / OpenShift : voir le
[README](../../README.md#kubernetes--openshift).

L'exemple complet est dans [`deploy/compose/`](../../deploy/compose/) :

| Fichier | Rôle |
|---|---|
| `compose.yaml` | Kansabooj (image publiée) + PostgreSQL 16 |
| `.env.example` | mot de passe de la base, version de l'image, secrets référencés par la configuration |
| `config/apis.yml` | configuration principale (serveur, notifiers, APIs) |
| `config/apis.d/*.yml` | fragments d'équipe (`groups` et `apis` uniquement) |
| `secrets/` | fichiers secrets lus via `${file:/run/secrets/kansabooj/…}` (certificats mTLS, CA d'entreprise) |

## Démarrer

Il faut Docker avec le plugin Compose (ou Podman avec `podman-compose`), et l'accès au registre `git.k3nny.fr`
(`docker login git.k3nny.fr` si le paquet n'est pas public).

```bash
cp -r deploy/compose /opt/kansabooj && cd /opt/kansabooj
cp .env.example .env && chmod 600 .env     # renseigner POSTGRES_PASSWORD et les secrets
$EDITOR config/apis.yml                    # vos APIs, vos notifiers
docker compose run --rm --no-deps kansabooj validate   # vérifie la configuration (variables comprises)
docker compose up -d
docker compose ps                          # kansabooj doit passer « healthy »
```

L'interface répond sur `http://127.0.0.1:8080`. Au premier démarrage, Kansabooj crée ses tables dans la base, puis
vérifie chaque API dans les minutes qui suivent (décalage aléatoire pour ne pas tout lancer en même temps).

```yaml
# compose.yaml (extrait)
  kansabooj:
    image: git.k3nny.fr/k3nny/kansabooj:${KANSABOOJ_VERSION:-v0.6.1}
    env_file: [.env]
    environment:
      KANSABOOJ_CONFIG: /etc/kansabooj/apis.yml
      KANSABOOJ_DB: postgres://kansabooj:${POSTGRES_PASSWORD}@db:5432/kansabooj?sslmode=disable
    volumes:
      - ./config:/etc/kansabooj:ro
      - ./secrets:/run/secrets/kansabooj:ro
    ports: ["127.0.0.1:8080:8080"]
    healthcheck:
      test: ["CMD", "/usr/local/bin/kansabooj", "healthcheck"]
    read_only: true
    user: "65532:65532"
    cap_drop: [ALL]
```

## Secrets

Aucun secret dans `config/apis.yml` : il les référence en `${NOM}`, et leurs valeurs sont dans `.env` (jamais
versionné). Exemple :

```yaml
# config/apis.yml
notifiers:
  - name: paiement-chat
    type: googlechat
    url: ${GCHAT_WEBHOOK_PAIEMENT}
```

```bash
# .env
GCHAT_WEBHOOK_PAIEMENT=https://chat.googleapis.com/v1/spaces/…
```

Une variable référencée mais absente fait échouer le démarrage avec un message qui la nomme (utiliser `${NOM:-}` si
elle est facultative). Les fichiers (certificat client, CA) vont dans `secrets/` et se référencent en
`${file:/run/secrets/kansabooj/client.pem}`.

## Publier l'interface

Le port n'est ouvert que sur `127.0.0.1` : placez un reverse proxy devant (TLS, et idéalement SSO).

- **Avec un SSO** (oauth2-proxy, Authelia, Keycloak…) : le proxy authentifie l'utilisateur et transmet son
  identifiant dans `X-Forwarded-User` (`server.user_header`). Il doit **remplacer** tout en-tête de ce nom envoyé
  par le client. Ajoutez `server.require_identity: true` pour refuser tout appel sans identité, et
  `server.admins` pour ceux qui peuvent recharger la configuration depuis l'interface.
- **Sans SSO** : ne publiez pas l'interface en accès libre. Activez `server.require_identity: true` et donnez aux
  appelants des jetons d'API (`kansabooj token new <nom> --scopes read`, cf. [guide CI](ci.md)).

Exemple Caddy (TLS automatique, authentification déléguée à un fournisseur `forward_auth`) :

```caddyfile
kansabooj.example.com {
    request_header -X-Forwarded-User      # jamais l'en-tête fourni par le client
    forward_auth authelia:9091 {
        uri /api/authz/forward-auth
        copy_headers Remote-User>X-Forwarded-User
    }
    reverse_proxy 127.0.0.1:8080
}
```

Renseignez aussi `server.base_url` (adresse publique) : c'est elle qui sert aux liens des notifications.

## Proxy sortant

Si les appels vers les APIs et les webhooks doivent passer par un proxy d'entreprise, ajoutez dans `.env` :

```bash
HTTPS_PROXY=http://proxy.interne.example:3128
NO_PROXY=localhost,127.0.0.1,db
```

ou déclarez-le dans `config/apis.yml` (`http.proxy`, profils réseau pour les APIs internes). Une CA d'interception
se déclare avec `http.ca_bundle` pointant vers un fichier de `secrets/`.

## Modifier la configuration

Éditez `config/apis.yml` ou un fragment de `config/apis.d/` : Kansabooj recharge la configuration tout seul dans la
minute. Une configuration invalide est refusée et l'ancienne reste active (le motif est dans les logs). Pour
recharger immédiatement :

```bash
docker compose kill -s HUP kansabooj
```

Une API retirée de la configuration est désactivée, pas supprimée : son historique reste consultable.

## Mettre à jour

```bash
sed -i 's/^KANSABOOJ_VERSION=.*/KANSABOOJ_VERSION=v0.7.0/' .env
docker compose pull kansabooj && docker compose up -d kansabooj
```

Les migrations de base s'appliquent au démarrage. Revenir à une version précédente n'annule pas une migration :
sauvegardez la base avant une mise à jour.

## Sauvegarder

Tout l'état (historique, versions des specs, acquittements, notifications) est dans PostgreSQL ; la configuration
est dans `config/` (versionnez-la dans Git).

```bash
docker compose exec -T db pg_dump -U kansabooj -Fc kansabooj > kansabooj-$(date +%F).dump
# restauration dans une base vide :
docker compose exec -T db pg_restore -U kansabooj -d kansabooj --clean --if-exists < kansabooj-2026-11-14.dump
```

## Utiliser une base PostgreSQL existante

Supprimez le service `db` et son volume, retirez `depends_on`, puis :

- soit `KANSABOOJ_DB=postgres://…` dans `.env` ;
- soit une section `database:` dans `config/apis.yml` (TLS, schéma dédié, compte sans droits DDL), décrite dans le
  [guide PostgreSQL externe](postgresql-externe.md).

## Superviser

- `docker compose ps` : état du healthcheck (`kansabooj healthcheck` interroge `/readyz`, qui vérifie la base).
- `docker compose logs -f kansabooj` : logs JSON.
- `GET /metrics` : métriques Prometheus, alertes suggérées dans le [guide d'exploitation](exploitation.md).
- Page **Statut** de l'interface : APIs en échec et prochains checks.

## Essai rapide depuis les sources

`deploy/compose.yaml` (à la racine de `deploy/`) construit l'image depuis le dépôt et utilise la configuration de
démo, sans secret : `docker compose -f deploy/compose.yaml up --build`. Il sert à essayer, pas à déployer.
