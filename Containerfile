# Kansabooj — image de production (multi-stage).
# Base images paramétrables : en environnement OpenShift / registry interne, remplacer par des images UBI
# (ex. --build-arg RUNTIME_IMAGE=registry.access.redhat.com/ubi9/ubi-micro).
ARG NODE_IMAGE=docker.io/library/node:22-alpine
ARG GO_IMAGE=docker.io/library/golang:1.26
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM ${NODE_IMAGE} AS ui
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM ${GO_IMAGE} AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=ui /src/frontend/dist ./internal/httpapi/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X git.k3nny.fr/kansabooj/internal/httpapi.Version=${VERSION}" \
      -o /out/kansabooj ./cmd/kansabooj

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/kansabooj /usr/local/bin/kansabooj
# Configuration de démo par défaut : en Kubernetes, monter un ConfigMap sur /etc/kansabooj.
COPY config/apis.demo.yml /etc/kansabooj/apis.yml
ENV KANSABOOJ_CONFIG=/etc/kansabooj/apis.yml \
    KANSABOOJ_LISTEN=:8080
# Aucune écriture disque : état en PostgreSQL (KANSABOOJ_DB), logs JSON sur stdout.
# OpenShift impose un UID arbitraire : rien dans l'image ne dépend de l'UID.
USER 65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kansabooj"]
CMD ["serve"]
