.PHONY: setup build build-app run open native-worker dev dev-backend dev-frontend test check clean release-audit release-check

setup:
	./scripts/setup.sh

build:
	./scripts/build_full.sh

# 底层入口，允许构建不含媒体运行库的应用，供框架与交叉编译检查使用。
build-app:
	./scripts/build.sh

run: build
	./scripts/open_desktop.sh

open:
	./scripts/open_desktop.sh

native-worker:
	./scripts/build_native_worker.sh

# 示例配置描述完整随包发行候选；当前开发构建缺媒体包时会如实给出问题。
release-audit:
	go run ./cmd/release-audit -config "$(or $(AUDIT_CONFIG),build/release-audit.example.json)"

release-check:
	go test -tags production .
	go run ./cmd/release-audit -config "$(or $(AUDIT_CONFIG),build/release-audit.example.json)" -check

dev:
	./scripts/dev.sh

dev-backend:
	go run ./cmd/devserver -listen 127.0.0.1:43110 -data work/dev-data

dev-frontend:
	pnpm --dir frontend run dev

test:
	go test ./internal/... ./cmd/...
	pnpm --dir frontend test

check:
	go vet ./internal/... ./cmd/...
	pnpm --dir frontend run typecheck
	pnpm --dir frontend run format:check
