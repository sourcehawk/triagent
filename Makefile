.PHONY: build build-go build-launcher build-mcp test test-go test-frontend test-e2e lint fmt frontend frontend-dev docs docs-dev docs-pages release-check release-snapshot-quick release-snapshot clean

# Build the embedded frontend bundle, then both Go binaries. Order matters:
# `frontend` syncs into internal/web/dist/, which the launcher embeds via
# //go:embed at compile time — so the Go build must run after the sync.
build: frontend build-go

# Go-only build for when internal/web/dist/ is already fresh (or you don't
# care that the embedded bundle is stale). Skips npm + next.
build-go: build-launcher build-mcp

build-launcher:
	go build -o bin/triagent ./cmd/triagent

build-mcp:
	go build -o bin/triagent-mcp ./cmd/triagent-mcp

# Wholesale test run: Go race suite + frontend vitest. CI runs both, so
# use this before claiming an implementation done. The per-language
# targets (`make test-go`, `make test-frontend`) stay available when you
# only need one half.
test: test-go test-frontend

test-go:
	go test -race -count=1 ./...

test-frontend:
	cd frontend && npm ci && npm test -- --run

# End-to-end suite: drives the real triagent binary against scripted
# claude/gh stubs. Gated behind the `e2e` build tag so it stays out of the
# fast `make test` loop; CI runs it only on PRs to main. The browser
# package install is guarded — a no-op until #16 adds e2e/browser, so the
# Go suite runs today without a package.json on disk.
#
# Depends on `frontend` so internal/web/dist/ holds the real Next.js bundle
# before the harness `go build`s ./cmd/triagent and embeds it via
# //go:embed all:dist. Without this the launcher serves a directory listing
# instead of the SPA, and the Playwright browser test sees an empty page.
#
# E2E_GOFLAGS is appended to `go test` — CI passes `-v` so the logs show every
# RUN/PASS/SKIP line and the Playwright output (which the harness pipes via
# t.Logf), making a silent skip (e.g. the k8s flow when envtest assets are
# missing) visible instead of hiding inside a package-level `ok`.
test-e2e: frontend
	@if [ -f e2e/browser/package.json ]; then cd e2e/browser && npm install; fi
	go test -tags=e2e -count=1 $(E2E_GOFLAGS) ./e2e/...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

# Build the embedded frontend bundle and sync it into internal/web/dist/,
# where //go:embed picks it up at Go-compile time. Next's static export
# lands in frontend/out/; we mirror that tree into dist/ and restore the
# tracked .gitkeep so the embed still has at least one match on a fresh
# `make clean` checkout.
frontend:
	cd frontend && npm ci && npm run build
	rm -rf internal/web/dist
	mkdir -p internal/web/dist
	cp -a frontend/out/. internal/web/dist/
	touch internal/web/dist/.gitkeep

# Local frontend dev server (no embedding).
frontend-dev:
	cd frontend && npm run dev

# Build the public docs site (consumes docs/content/*.md, emits a static
# export to docs/site/out/). No basePath — suitable for serving the
# output locally (e.g. `npx serve docs/site/out`).
docs:
	cd docs/site && npm install && npm run build

# Local docs dev server on http://localhost:3030.
docs-dev:
	cd docs/site && npm install && npm run dev

# Build the docs site for GitHub Pages deployment. Mirrors what the
# .github/workflows/docs.yml workflow does — useful for previewing
# what Pages will actually serve before pushing.
docs-pages:
	cd docs/site && npm install && DOCS_BASE_PATH=/triagent npm run build
	touch docs/site/out/.nojekyll

# Validate .goreleaser.yaml without building anything. Cheap; run before
# pushing a tag.
release-check:
	goreleaser check

# Fast local sanity build: current host platform only, skips archives /
# homebrew / changelog. Use during config iteration. Depends on `frontend`
# so the embedded bundle is fresh — otherwise the binary ships an empty
# UI. Output lands under dist/.
release-snapshot-quick: frontend
	goreleaser build --snapshot --clean --single-target

# Full snapshot release dry-run: builds every (goos, goarch) combo,
# produces archives + checksums + Homebrew cask, but skips publishing
# to GitHub / the tap. Mirrors what the release workflow does on a tag
# push, minus the upload. Output lands under dist/.
release-snapshot: frontend
	goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf bin internal/web/dist/* internal/web/dist/.gitkeep dist
	mkdir -p internal/web/dist
	touch internal/web/dist/.gitkeep
