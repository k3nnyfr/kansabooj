# ADR 0005 — Chart Helm à côté des manifests kustomize

**Statut** : accepté — 2026-11-15

## Contexte
Les manifests kustomize (`deploy/kubernetes`) couvrent Kubernetes et OpenShift, mais une partie des équipes
d'exploitation déploie tout par Helm (catalogue de charts, Argo CD / Flux en mode Helm, valeurs par environnement)
et attend des points d'extension usuels : labels et annotations de conformité sur chaque ressource, sélection du
routeur, ConfigMap fourni par un autre dépôt. Sur OpenShift, exposer par une Route impose un objet propre à la
plateforme ; le contrôleur Ingress-to-route sait produire cette Route depuis un Ingress standard.

## Décision
- Un chart `deploy/helm/kansabooj`, **équivalent fonctionnel** des manifests kustomize (mêmes sondes, sécurité,
  volumes, variables), sans dépendance à un autre chart.
- **Exposition par un Ingress uniquement** : sur OpenShift, `ingressClassName: openshift-default` et le contrôleur
  Ingress-to-route crée la Route (annotations `route.openshift.io/*` et `haproxy.router.openshift.io/*` reportées) ;
  sur Kubernetes, n'importe quel contrôleur, l'exemple fourni étant HAProxy. Aucun objet `Route` dans le chart :
  un seul chemin à maintenir, et aucune API propre à OpenShift requise pour l'installer.
- Le chart ne crée **ni base ni Secret** (principe 5 : les secrets restent gérés par l'exploitation).
- Version du chart = version de l'application (même dépôt, même cycle de release).
- Kustomize est conservé (overlays, composants) : les deux sont tenus alignés, la CI rend les deux.

## Conséquences
- Toute évolution des manifests (variable, volume, sonde, sécurité) se reporte dans les deux (`deploy/kubernetes`
  et `deploy/helm`) ; `task helm:lint` et le rendu kustomize tournent en CI.
- La Route générée n'est pas modifiable à la main (écrasée par le contrôleur) : ses réglages passent par l'Ingress.
- Pas de terminaison `reencrypt` / `passthrough` : Kansabooj sert du HTTP derrière le routeur.
