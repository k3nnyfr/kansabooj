# ADR 0004 — PostgreSQL pour le déploiement Kubernetes / OpenShift, SQLite pour le local

**Statut** : accepté — 2026-11-14 (remplace le choix « SQLite » de l'architecture initiale)

## Contexte
La cible de déploiement est principalement OpenShift / Kubernetes. SQLite y impose :
- un PersistentVolume en `ReadWriteOnce` → **une seule réplique**, stratégie de déploiement `Recreate`
  (coupure à chaque mise à jour), pas de haute disponibilité ;
- des sauvegardes au niveau volume, hors des outils standards des équipes d'exploitation ;
- une adhérence au système de fichiers du pod, contraire au modèle « conteneur sans état ».

## Décision
- **PostgreSQL (≥ 12) est la base de production.** Le conteneur est sans état : configuration en ConfigMap,
  secrets en Secret, état en base, logs sur stdout, système de fichiers racine en lecture seule.
- **SQLite reste supporté** pour la CLI, le développement local et les tests rapides : même code, même SQL
  (portable), migrations parallèles `migrations/sqlite` et `migrations/postgres` numérotées à l'identique.
- **Plusieurs réplicas** : un verrou consultatif PostgreSQL (`pg_try_advisory_lock`) élit une instance *leader* qui
  planifie les checks, rejoue les notifications et purge les journaux ; toutes les instances servent l'interface,
  l'API et les checks manuels. Un verrou par API empêche deux checks simultanés de la même API, et une mise à jour
  optimiste (`current_snapshot_id IS NOT DISTINCT FROM …`) protège l'enregistrement d'une nouvelle version.
- La base est fournie par l'opérateur de la plateforme (CloudNativePG, Crunchy PGO, base managée) ; Kansabooj ne
  consomme qu'une URL de connexion (`KANSABOOJ_DB`).

## Conséquences
- + Mises à jour sans coupure (RollingUpdate), haute disponibilité, sauvegardes/PITR standards.
- + Rechargement de configuration à chaud quand le ConfigMap ou un Secret monté change (détection par empreinte).
- − Deux dialectes à maintenir : toute requête doit passer les tests sur les deux bases
  (`task test` et `task test:pg`, à exécuter en CI avec un service PostgreSQL).
- − Les specs sont stockées en `BYTEA` compressé : volume modeste (quelques Mo par version distincte d'une grosse spec).
