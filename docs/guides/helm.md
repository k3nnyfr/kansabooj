# Déploiement avec Helm (Kubernetes, OpenShift)

Le chart `deploy/helm/kansabooj` déploie la même chose que les manifests kustomize de `deploy/kubernetes` :
conteneur sans état, racine en lecture seule, UID arbitraire accepté (OpenShift), plusieurs réplicas dont un seul
planifie les checks, configuration rechargée à chaud. Il ne crée ni base ni Secret : ils sont fournis par
l'exploitation (CloudNativePG, base managée, SealedSecrets, External Secrets, Vault…).

```bash
helm upgrade --install kansabooj deploy/helm/kansabooj -n kansabooj --create-namespace \
  -f deploy/helm/examples/openshift.yaml -f mes-valeurs.yaml
```

| Exemple | Usage |
|---|---|
| [`examples/openshift.yaml`](../../deploy/helm/examples/openshift.yaml) | OpenShift : Ingress converti en Route, annotations du routeur HAProxy, sharding, NetworkPolicy |
| [`examples/kubernetes-haproxy.yaml`](../../deploy/helm/examples/kubernetes-haproxy.yaml) | Kubernetes avec le contrôleur HAProxy Kubernetes Ingress (classe `haproxy`) |
| [`examples/external-db.yaml`](../../deploy/helm/examples/external-db.yaml) | PostgreSQL sur un serveur tiers, migration par le compte propriétaire en hook Helm |

Rendu sans cluster : `task helm:render VALUES=deploy/helm/examples/openshift.yaml` ; contrôle : `task helm:lint`.

## Prérequis

| Secret | Clé | Quand |
|---|---|---|
| `kansabooj-db-app` | `uri` (URL PostgreSQL) | `database.mode: url` (défaut) ; généré par CloudNativePG (`deploy/kubernetes/postgres/cluster.yaml`) |
| `kansabooj-db-credentials` | `password` | `database.mode: config` (base décrite dans `apis.yml`) |
| `kansabooj-db-ca` | `ca.pem` | optionnel, `sslrootcert: /etc/kansabooj-db/ca.pem` |
| `kansabooj-db-owner` | `uri` (compte propriétaire) | `database.migrateJob.enabled: true` |
| `kansabooj-secrets` | une clé par variable | optionnel : webhooks, clés partenaires référencés en `${VAR}` dans `apis.yml` |
| `kansabooj-files` | un fichier par clé | optionnel : certificats mTLS, secrets OAuth2 lus par `${file:/run/secrets/kansabooj/…}` |

Les noms se changent dans les valeurs (`database.*`, `secrets.envFrom`, `secrets.files` ; `""` pour ne rien monter).

## Configuration

`config.apisYml` contient le fichier `apis.yml` (ConfigMap géré par le chart). Pour une configuration maintenue dans
son propre dépôt et poussée par GitOps, `config.existingConfigMap` référence un ConfigMap existant (clé `apis.yml`).
Le nom du ConfigMap est stable : un `helm upgrade` qui ne change que la configuration ne redémarre pas les pods,
Kansabooj détecte la modification (sous 30 s, plus le délai de propagation du kubelet) et se recharge. Une
configuration invalide est refusée et l'ancienne conservée : vérifier les logs, ou la valider avant
(`kansabooj validate`, guide [CI](ci.md)).

## OpenShift : Ingress-to-route

Le chart ne crée pas de Route : il crée un Ingress standard, que le contrôleur Ingress-to-route d'OpenShift
convertit en Route (et tient à jour : une modification faite directement sur la Route générée est écrasée,
tout passe par les valeurs `ingress.*`).

```yaml
ingress:
  enabled: true
  className: openshift-default            # IngressClass du contrôleur Ingress-to-route
  labels:
    type: internal                        # sharding du routeur
  annotations:
    route.openshift.io/termination: edge
    haproxy.router.openshift.io/timeout: 60s
  hosts:
    - host: kansabooj.apps.example.com
  tls:
    - {}                                  # certificat par défaut du routeur
```

- **TLS** : `tls: [{}]` donne une Route `edge` avec le certificat par défaut du routeur (wildcard `*.apps`) ;
  `tls: [{ hosts: [...], secretName: ... }]` utilise le certificat d'un Secret `kubernetes.io/tls`. Kansabooj sert
  du HTTP : la terminaison est `edge` (ni `reencrypt`, ni `passthrough`).
- **Annotations** : le routeur OpenShift est un HAProxy ; ses réglages par Route (`haproxy.router.openshift.io/timeout`,
  `hsts_header`, `ip_allowlist` — `ip_whitelist` avant OpenShift 4.14 —, `rate-limit-connections`…) se posent sur
  l'Ingress et sont reportés sur la Route.
- **Sharding** : les labels de `ingress.labels` sont reportés sur la Route ; ils servent au `routeSelector` d'un
  IngressController dédié (routeur interne, par exemple). Contrôle : `oc get route -l app.kubernetes.io/instance=kansabooj -o yaml`.
- **NetworkPolicy** : l'entrée depuis le routeur se reconnaît au label de namespace
  `policy-group.network.openshift.io/ingress` (voir l'exemple).

## Kubernetes : HAProxy Ingress

Avec le contrôleur [HAProxy Kubernetes Ingress](https://www.haproxy.com/documentation/kubernetes-ingress/)
(`haproxytech/kubernetes-ingress`) : `className: haproxy` et ses annotations `haproxy.org/*` (`ssl-redirect`,
`timeout-server`, `allow-list`, `hsts`…). Le chart n'impose aucun contrôleur ni annotation : toute autre classe
fonctionne en changeant `ingress.className` et `ingress.annotations`.

## Identité des utilisateurs

L'acquittement des changements et le journal utilisent l'identité posée par le proxy SSO (en-tête
`X-Forwarded-User` par défaut, `server.user_header`). Placer l'Ingress derrière oauth2-proxy ou l'authentification
du contrôleur ; sans proxy, les actions sont attribuées à `anonymous`.

## Métadonnées

| Valeur | Portée |
|---|---|
| `commonLabels`, `commonAnnotations` | toutes les ressources du chart |
| `podLabels`, `podAnnotations` | pods (ex. `prometheus.io/scrape`) |
| `deployment.*`, `service.*`, `serviceAccount.*`, `config.*`, `ingress.*` | `labels` et `annotations` propres à chaque ressource, prioritaires sur les communes |
| `database.migrateJob.annotations` | Job de migration |

## Base sur un serveur tiers

`database.mode: config` : la section `database:` de `apis.yml` s'applique, avec le mot de passe en
`KANSABOOJ_DB_PASSWORD` et la CA montée dans `/etc/kansabooj-db`. Si le compte applicatif n'a pas les droits DDL
(`migrate: false`), `database.migrateJob.enabled: true` lance `kansabooj migrate` avec le compte propriétaire en
hook `pre-install` / `pre-upgrade` : la mise à jour ne se poursuit que si la migration réussit. Détails :
[PostgreSQL externe](postgresql-externe.md).

## Supervision

`/metrics` sur le port 8080 (métriques et alertes proposées : [exploitation](exploitation.md)). Avec le Prometheus
de la plateforme, un ServiceMonitor peut viser le Service (port `http`) ; avec un Prometheus par annotations,
`podAnnotations` suffit.
