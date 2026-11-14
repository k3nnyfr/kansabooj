#!/usr/bin/env bash
# Régénère les captures d'écran du README (docs/images/*.png) sur des APIs FICTIVES, sans réseau :
#   - scripts/screenshots/site/ : specs et pages de doc inventées ; un mini proxy HTTP local les sert sous des
#     hôtes *.example (Kansabooj l'utilise comme proxy sortant, cf. scripts/screenshots/apis.yml) ;
#   - quatre « passes » de checks simulent un mois d'historique (dates réécrites dans une base SQLite jetable) ;
#   - captures avec Chromium headless.
# Prérequis : bin/kansabooj (task build), python3, curl, Chromium ou chrome-headless-shell (CHROME=… sinon).
# Usage : task screenshots   (KEEP=1 conserve le dossier de travail)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC="$ROOT/scripts/screenshots/site"
CONFIG="$ROOT/scripts/screenshots/apis.yml"
OUT="$ROOT/docs/images"
BIN="$ROOT/bin/kansabooj"
PROXY_PORT=${PROXY_PORT:-18765}
UI_PORT=${UI_PORT:-18766}

[ -x "$BIN" ] || { echo "bin/kansabooj absent : lancer d'abord « task build »" >&2; exit 1; }
CHROME=${CHROME:-$(command -v chromium || command -v chromium-browser || command -v google-chrome ||
  find "$HOME/.cache/ms-playwright" -name chrome-headless-shell -type f 2>/dev/null | head -1 || true)}
[ -n "$CHROME" ] || { echo "Chromium introuvable : CHROME=/chemin/vers/chromium" >&2; exit 1; }

WORK=$(mktemp -d)
WWW="$WORK/www"
DB="$WORK/kansabooj.db"
export SHOT_PROXY="http://127.0.0.1:$PROXY_PORT"
PIDS=()
cleanup() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done
  if [ -n "${KEEP:-}" ]; then echo "conservé : $WORK"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

# Proxy HTTP minimal : « GET http://hôte/chemin » → $WWW/hôte/chemin (index.html pour un dossier).
mkdir -p "$WWW" "$OUT"
python3 - "$WWW" "$PROXY_PORT" >/dev/null 2>&1 <<'EOF' &
import functools, http.server, sys, urllib.parse
root, port = sys.argv[1], int(sys.argv[2])
class H(http.server.SimpleHTTPRequestHandler):
    def translate_path(self, path):
        u = urllib.parse.urlsplit(path)
        return super().translate_path("/" + u.netloc + (u.path or "/"))
http.server.ThreadingHTTPServer(("127.0.0.1", port), functools.partial(H, directory=root)).serve_forever()
EOF
PIDS+=($!)

# Last-Modified croissant à chaque publication : les passes s'enchaînent dans la même seconde
# et le serveur répondrait 304 au If-Modified-Since de Kansabooj.
STAMP=$(date +%s)
publish() { # publish <fichier source | -> <hôte/chemin>  (- : stdin)
  mkdir -p "$(dirname "$WWW/$2")"
  if [ "$1" = - ]; then cat > "$WWW/$2"; else cp "$SRC/$1" "$WWW/$2"; fi
  STAMP=$((STAMP + 60)); touch -d "@$STAMP" "$WWW/$2"
}

# max(id) par table avant une passe, pour ne redater que les lignes qu'elle a créées
marks() {
  python3 - "$DB" <<'EOF'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
print(" ".join(str(c.execute(f"select coalesce(max(id),0) from {t}").fetchone()[0]) for t in ("snapshots", "changes", "checks")))
EOF
}

# pass <jours dans le passé> <HH:MM UTC> : check de toutes les APIs, puis dates des nouvelles lignes réécrites
pass() {
  local before="0 0 0"
  [ -f "$DB" ] && before=$(marks)
  "$BIN" check --all --config "$CONFIG" --db "$DB" >/dev/null 2>&1 || true # une API en erreur → code ≠ 0
  python3 - "$DB" "$1" "$2" $before <<'EOF'
import sqlite3, sys, datetime as dt
db, days, hour = sys.argv[1], int(sys.argv[2]), sys.argv[3]
s, c, k = map(int, sys.argv[4:7])
h, m = map(int, hour.split(":"))
when = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=days)).replace(hour=h, minute=m, second=12, microsecond=0)
ts = when.strftime("%Y-%m-%d %H:%M:%S.000000000 +0000 UTC")
con = sqlite3.connect(db)
con.execute("update snapshots set fetched_at=? where id>?", (ts, s))
con.execute("update changes set detected_at=? where id>?", (ts, c))
con.execute("update checks set started_at=? where id>?", (ts, k))
con.execute("update apis set last_checked_at=?", (ts,))
con.commit()
EOF
}

PAYFLOW=api.payflow.example/v3/openapi.yaml
COLIS=dev.colis-express.example
METEO=www.meteo-pro.example/api/documentation.html
CLIENTS=referentiel-clients.interne.example/openapi.yaml
ADRESSES=api.adresses.example/openapi.json

# J-34 : première capture de tout le parc
publish payflow/v1.yaml $PAYFLOW
publish kdo/swagger.json developers.kdoplus.example/swagger.json
publish colis/docs/index.html $COLIS/docs/index.html
publish colis/docs/swagger-initializer.js $COLIS/docs/swagger-initializer.js
publish colis/v1.json $COLIS/openapi.json
publish clients/v1.yaml $CLIENTS
publish meteo/v1.html $METEO
publish - $ADRESSES <<'EOF'
{"openapi":"3.0.3","info":{"title":"Normalisation d'adresses","version":"2.0.1"},
 "paths":{"/addresses/normalize":{"post":{"summary":"Normaliser une adresse","responses":{"200":{"description":"Adresse normalisée"}}}}}}
EOF
pass 34 08:14
# J-12 : PayFlow 3.0.0 (rupture dans le périmètre réexposé), Colis Express 5.3.0 ;
#        Météo Pro régénère sa page sans la modifier (date ignorée par text_ignore)
publish payflow/v2.yaml $PAYFLOW
publish colis/v2.json $COLIS/openapi.json
sed 's/2026-10-14 08:00/2026-11-02 08:00/' "$SRC/meteo/v1.html" | publish - $METEO
pass 12 09:02
# J-3 : Référentiel clients 1.13.0, Météo Pro change sa documentation
publish clients/v2.yaml $CLIENTS
publish meteo/v2.html $METEO
pass 3 13:41
# Aujourd'hui : PayFlow 3.1.0 (ajouts), l'API d'adresses a disparu
publish payflow/v3.yaml $PAYFLOW
rm -f "$WWW/$ADRESSES"
pass 0 "$(date -u -d '-25 min' +%H:%M)"

# Nom d'instance affiché par la page Statut : un nom de pod fictif plutôt que celui de la machine
# (espace de noms UTS sans root ; sinon, le vrai nom d'hôte apparaîtra dans status.png).
SERVE=("$BIN" serve --config "$CONFIG" --db "$DB" --listen "127.0.0.1:$UI_PORT")
if unshare -ru true 2>/dev/null; then
  SERVE=(unshare -ru sh -c 'hostname kansabooj-6f8d9c7b54-x2k4q && exec "$@"' sh "${SERVE[@]}")
else
  echo "attention : unshare indisponible, status.png montrera le nom de cette machine" >&2
fi
"${SERVE[@]}" >"$WORK/serve.log" 2>&1 &
PIDS+=($!)
for _ in $(seq 50); do curl -sf "http://127.0.0.1:$UI_PORT/readyz" >/dev/null && break; sleep 0.2; done

# Le changement de Colis Express a été pris en compte par l'équipe : il apparaît comme vu
id=$(python3 - "$DB" <<'EOF'
import sqlite3, sys
print(sqlite3.connect(sys.argv[1]).execute(
    "select max(id) from changes where api_id='colis-express' and from_snapshot_id is not null").fetchone()[0])
EOF
)
curl -sf -X POST -H 'X-Forwarded-User: marie.dupont' "http://127.0.0.1:$UI_PORT/api/changes/$id/ack" >/dev/null

shot() { # shot <fichier> <chemin> <hauteur> [options Chromium…]
  "$CHROME" --headless --no-sandbox --hide-scrollbars --force-device-scale-factor=1 --lang=fr-FR "${@:4}" \
    --window-size="1280,$3" --virtual-time-budget=5000 --screenshot="$OUT/$1" \
    "http://127.0.0.1:$UI_PORT$2" >/dev/null 2>&1
  echo "  docs/images/$1"
}
echo "Captures :"
shot apis.png "/" 790
shot apis-groups.png "/?by=group" 980
shot api-breaking.png "/apis/payflow" 1010
shot api-discovery.png "/apis/colis-express" 630
shot api-html.png "/apis/meteo-pro" 735
shot status.png "/status" 480
shot compare.png "/apis/payflow/compare" 1100
# Documentation de Kdo+ (Swagger 2.0, timeline courte : la section est visible sans défiler), clair puis sombre
doc=$(python3 - "$DB" <<'EOF'
import sqlite3, sys
print(sqlite3.connect(sys.argv[1]).execute(
    "select max(id) from snapshots where api_id='kdo-plus'").fetchone()[0])
EOF
)
shot api-doc.png "/apis/kdo-plus?doc=$doc" 700
shot api-doc-dark.png "/apis/kdo-plus?doc=$doc" 700 --blink-settings=preferredColorScheme=0
