# PostgreSQL sur un serveur tiers

Kansabooj peut utiliser n'importe quel PostgreSQL ≥ 12 : base gérée par une équipe DBA, service managé, cluster
partagé. Trois façons de le désigner, par ordre de priorité :

1. `--db` / variable `KANSABOOJ_DB` : URL complète `postgres://user:pass@host:5432/base?sslmode=verify-full&sslrootcert=…`
   (ou le mot-clé `postgres` pour utiliser les variables libpq `PGHOST`, `PGUSER`, `PGPASSWORD`…) ;
2. la section `database:` de `apis.yml` (recommandé pour un serveur tiers : paramètres lisibles, mot de passe en `${…}`) ;
3. à défaut, une base SQLite locale `data/kansabooj.db` (développement, CLI).

Les réglages `schema`, `application_name` et de pool de la section `database:` s'appliquent dans les cas 1 et 2.

## Configuration type

```yaml
database:
  host: pg-prod.dba.interne.example
  name: apis_shared
  user: kansabooj_app
  password: ${KANSABOOJ_DB_PASSWORD}     # ou ${file:/run/secrets/kansabooj/db-password}
  sslmode: verify-full                   # défaut : verify-full si sslrootcert, sinon require
  sslrootcert: /etc/kansabooj-db/ca.pem  # CA du certificat du serveur
  # sslcert / sslkey : certificat client si le serveur l'exige
  schema: kansabooj                      # schéma dédié sur une base partagée (search_path)
  migrate: false                         # le compte applicatif n'a pas les droits DDL
  max_open_conns: 10                     # ≥ 4
  conn_max_lifetime: 30m                 # recyclage (PgBouncer, bascules, répartiteurs)
```

- Plusieurs instances Kansabooj peuvent partager une base avec des **schémas différents** : les verrous
  (leader, migrations, checks) sont préfixés par le schéma.
- Une modification de la section `database:` nécessite un redémarrage (pas de rechargement à chaud de la connexion).

## Comptes séparés (DBA)

Le compte applicatif n'a que les droits DML ; le propriétaire du schéma applique les migrations.

```sql
CREATE ROLE kansabooj_owner LOGIN PASSWORD '…';
CREATE ROLE kansabooj_app   LOGIN PASSWORD '…';
CREATE SCHEMA kansabooj AUTHORIZATION kansabooj_owner;
GRANT USAGE ON SCHEMA kansabooj TO kansabooj_app;
ALTER DEFAULT PRIVILEGES FOR ROLE kansabooj_owner IN SCHEMA kansabooj
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO kansabooj_app;
ALTER DEFAULT PRIVILEGES FOR ROLE kansabooj_owner IN SCHEMA kansabooj
  GRANT USAGE, SELECT ON SEQUENCES TO kansabooj_app;
```

Migration (avant chaque montée de version), avec l'identité du propriétaire et le reste de la configuration :

```bash
KANSABOOJ_DB='postgres://kansabooj_owner:…@pg-prod.dba.interne.example:5432/apis_shared?sslmode=verify-full&sslrootcert=/etc/kansabooj-db/ca.pem' \
  kansabooj migrate --config apis.yml
```

Avec `migrate: false`, Kansabooj refuse de démarrer tant que des migrations sont en attente, avec un message explicite.
Avec `migrate: true` (défaut), les migrations s'exécutent au démarrage, sérialisées par un verrou consultatif
(plusieurs réplicas peuvent démarrer en même temps).

## Kubernetes / OpenShift

Composant kustomize `deploy/kubernetes/components/external-db` (exemple : overlay `openshift-external-db`) :
Secret `kansabooj-db-credentials` (clé `password`), Secret `kansabooj-db-ca` (clé `ca.pem`), et
`migrate-job.yaml` pour la migration par le compte propriétaire.

Scénario vérifié (2026-11-14, PostgreSQL 12) : TLS `verify-full` avec CA privée, base partagée, schéma dédié,
compte applicatif sans DDL refusé tant que le schéma n'est pas migré, migration par le propriétaire, CA étrangère rejetée.
