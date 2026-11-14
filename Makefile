.PHONY: validate check dev-backend dev-frontend test lint build clean

CONFIG ?= config/apis.demo.yml
DB     ?= data/kansabooj.db

validate:
	cd backend && go run ./cmd/kansabooj validate --config ../$(CONFIG)

# make check API=worldline-direct  |  make check API=--all  |  make check API="--dry-run partner-x"
check:
	cd backend && go run ./cmd/kansabooj check --config ../$(CONFIG) --db ../$(DB) $(API)

dev-backend:  # v0.2
	cd backend && go run ./cmd/kansabooj serve --config ../$(CONFIG) --db ../$(DB)

dev-frontend:
	cd frontend && npm run dev

test:
	cd backend && go test ./...
	cd frontend && npm test --if-present

lint:
	cd backend && golangci-lint run
	cd frontend && npm run lint --if-present

build:
	cd frontend && npm ci && npm run build
	rm -rf backend/internal/httpapi/dist && cp -r frontend/dist backend/internal/httpapi/dist
	cd backend && CGO_ENABLED=0 go build -o ../bin/kansabooj ./cmd/kansabooj

build-cli:
	cd backend && CGO_ENABLED=0 go build -o ../bin/kansabooj ./cmd/kansabooj

clean:
	rm -rf bin data frontend/dist backend/internal/httpapi/dist
