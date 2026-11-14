# Kansabooj dans la CI

Deux usages, avec un jeton d'API dédié par appelant (`kansabooj token new <nom> --scopes …`, hash ajouté dans
`server.tokens`, jeton stocké dans les variables protégées de la CI).

## 1. Dépôt de configuration : valider une MR avant merge

Chaque équipe possède un fragment (`apis.d/<équipe>.yml`, protégé par CODEOWNERS). La CI le fait valider par le
serveur **avec son contexte réel** (notifiers, groupes, profils réseau, variables d'environnement et secrets présents),
sans rien appliquer ni appeler les URLs déclarées. Scope requis : `validate`.

```yaml
# .gitlab-ci.yml (dépôt de configuration)
validate-kansabooj:
  image: curlimages/curl:8.11.1
  rules: [ { changes: ["apis.d/*.yml"] } ]
  script:
    - |
      for f in $(git diff --name-only "$CI_MERGE_REQUEST_DIFF_BASE_SHA" -- 'apis.d/*.yml'); do
        name=$(basename "$f")
        res=$(curl -sf -X POST -H "Authorization: Bearer $KANSABOOJ_TOKEN" -H 'Content-Type: application/yaml' \
              --data-binary @"$f" "$KANSABOOJ_SERVER/api/config/validate?fragment=$name")
        echo "$name: $res"
        echo "$res" | grep -q '"valid":true' || exit 1
      done
```

Hors ligne (sans serveur), `kansabooj validate --config apis.yml` vérifie la syntaxe et les références, mais pas
la présence des variables d'environnement et secrets du serveur.

## 2. Équipe productrice : bloquer une rupture de contrat avant déploiement

Pour une API interne surveillée, comparer la spec produite par le build à la version enregistrée par Kansabooj.
Le périmètre réexposé (`gateway.exposed_operations`) et les règles anti-bruit (`ignore`) de l'API sont appliqués
automatiquement. Scope requis : `read`.

```yaml
contract-check:
  image: registry.example/kansabooj:latest
  script:
    - kansabooj diff --fail-on error kansabooj:referentiel-clients build/openapi.yaml
  variables:
    KANSABOOJ_SERVER: https://kansabooj.interne.example
    # KANSABOOJ_TOKEN : variable protégée de la CI
```

Codes retour : `0` rien de bloquant, `1` changements de niveau ≥ `--fail-on` dans le périmètre, `2` erreur
technique. `--format json` produit un rapport exploitable (artefact, commentaire de MR). `--fail-on warn` est plus
strict ; `--fail-on none` ne fait que rapporter.

`kansabooj diff` compare aussi deux fichiers ou URLs quelconques (`kansabooj diff v1.yaml v2.yaml`), avec
`--exposed "GET /orders/{id}"` (répétable) et `--ignore '$.servers'` pour reproduire le filtrage du serveur.

## GitHub Actions (équivalent)

```yaml
- name: Contract check
  run: docker run --rm -v "$PWD:/w" -w /w -e KANSABOOJ_SERVER -e KANSABOOJ_TOKEN registry.example/kansabooj:latest \
         diff kansabooj:referentiel-clients build/openapi.yaml
  env:
    KANSABOOJ_SERVER: https://kansabooj.interne.example
    KANSABOOJ_TOKEN: ${{ secrets.KANSABOOJ_TOKEN }}
```
