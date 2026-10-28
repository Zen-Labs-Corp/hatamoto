# Hatamoto Core — self-contained build/deploy for hatamoto.dev ONLY.
#
# Deploys target the hatamoto stack on the deploy host:
#   service hatamoto-api  (:8090)   /opt/hatamoto/bin/hatamoto supervise (runs `serve`)
#   Postgres DB hatamoto_core
# Nothing else on the host is touched.
#
# The deploy host is an ssh host alias. It is never committed: set DEPLOY_HOST
# in the environment, or write the alias to the untracked file .deploy-host.
#
# Build toolchain is LOCAL (Go only, no Node); the box has no Go: the binary is
# built here and scp'd. One binary serves the site, the admin and the API.

.PHONY: need-staticcheck all plugins registry registry-guard registry-pull-archive build build-api release-build release-bundles need-host deploy upload-api install-unit check-unit restart-api migrate check budgets budgets-update vendor vendor-verify vuln licenses ui-lint ui-check help docs-publish docs-publish-test hooks hooks-off test-hooks

DEPLOY_HOST ?= $(shell cat .deploy-host 2>/dev/null)
REMOTE_DIR  := /opt/hatamoto
UNIT_PATH   := /etc/systemd/system/hatamoto-api.service
FORCE_UNIT  ?=

# Stamped into `hatamoto version`.
VERSION := $(shell sh scripts/version.sh)
COMMIT  := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
GO_LDFLAGS := -X hatamoto.dev/core/app.Version=$(VERSION) -X hatamoto.dev/core/app.Commit=$(COMMIT)


all: build

# --- Build ------------------------------------------------------------------
# WASM plugins under examples/plugins/<name>: plugin.wasm (standard Go, wasip1
# reactor) plus plugin.json, migrations/, ui/ and assets/, staged in <name>/build/ ready
# for `hatamoto plugin pack`. hello is the reference (datasource-demo the builder data source). The
# first-party plugins (gtm, searchconsole) live in the hatamoto-first-party monorepo and reach the registry through
# the plugins repo manifests, like every other plugin.
PLUGINS := hello datasource-demo

plugin-%:
	rm -rf examples/plugins/$*/build
	cd examples/plugins/$* && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
	cp examples/plugins/$*/plugin.json examples/plugins/$*/build/plugin.json
	for d in migrations ui assets; do if [ -d examples/plugins/$*/$$d ]; then cp -r examples/plugins/$*/$$d examples/plugins/$*/build/$$d; fi; done
	@echo "Built examples/plugins/$*/build/"

plugins: $(addprefix plugin-,$(PLUGINS))

# The registry: registry/packages/*.hmp signed with the "hatamoto first-party"
# key, and registry/registry.json (the signed index). The private key is never
# in the repository: point HATAMOTO_REGISTRY_KEY_FILE at it (CI uses a
# throwaway key from `hatamoto keys generate`, which builds the same files but
# is not trusted by any server). See registry/README.md.
#
# COMMUNITY_DIR is a checkout of the plugins repository (manifests/*.json); each
# approved manifest (first-party plugins included) is built from its pinned
# commit, packed and signed with the same key (registry/build-community.sh).
# Every run rebuilds the whole index, so `make registry` refuses to run without
# COMMUNITY_DIR. meta.json feeds author/source/tier into
# the signed index and from there into the /plugins page. The page command
# verifies the index like a server does, so a throwaway key (CI) is trusted for
# that step through the <key>.pub that `keys generate` writes next to it.
COMMUNITY_DIR ?=
# GitHub owners whose manifests may use tier first-party (space separated). It only labels the
# plugin on the page; trust is the signature. Fixed with override so a stray exported
# environment variable (or a command-line value by mistake) cannot widen it: to change the
# list deliberately, edit this line in a reviewed commit.
# zenlabscorp is dropped once the plugin manifests point at Zen-Labs-Corp (runbook 3.10).
override FIRST_PARTY_OWNERS := zenlabscorp Zen-Labs-Corp
COMMUNITY_SKIP ?=
# Theme names to leave out of the theme build (space separated; logged loudly), like COMMUNITY_SKIP.
THEMES_SKIP ?=
# Append-only archive of every signed community .hmp (never wiped by `make registry`;
# fill it with `make registry-pull-archive` before building, publish it with scripts/publish-registry.sh).
ARCHIVE_DIR ?= registry/archive
# Source mirror (https://github.com/<org>) the community plugins are built from before the authors' repos.
MIRROR_BASE ?= https://github.com/Zen-Labs-Corp
# 1 = build mirrors from their vendor/ (no go.sum check); only when upstream and the module proxy are gone. See registry/README.md.
TRUST_MIRROR_VENDOR ?=
REMOTE_REGISTRY ?= /opt/hatamoto/registry
# Community plugins are built with our own pinned toolchain (backend/go.mod), never the system one.
BUILD_GOTOOLCHAIN := $(shell awk '/^toolchain/{print $$2}' backend/go.mod)
# Links on the optional static plugin directory page (REGISTRY_PAGE=1). The default is derived from this
# checkout's origin remote (git@github.com:OWNER/REPO.git or https), so no
# repository owner is hard-coded; the plugins repo is OWNER/REPO-plugins.
# Override either variable on the command line.
REPO_WEB_URL ?= $(shell git remote get-url origin 2>/dev/null | sed -E -e 's|^git@([^:]+):|https://\1/|' -e 's|^https://[^@/]*@|https://|' -e 's|\.git$$||')
PLUGINS_REPO_URL ?= $(REPO_WEB_URL)-plugins/blob/main
REGISTRY_PAGE_ARGS ?= --title Plugins \
	--intro 'Extend Hatamoto. Every plugin here was reviewed and signed by the Hatamoto team.' \
	--write-url $(PLUGINS_REPO_URL)/WRITING.md --submit-url $(PLUGINS_REPO_URL)/CONTRIBUTING.md \
	--install-hint 'Install: Admin > Plugins > Browse registry'

# The page step verifies the index like a server: first-party key plus
# HATAMOTO_TRUSTED_KEYS. REGISTRY_PAGE_TRUST_SIGNER=1 (CI, throwaway key) also
# trusts the <key>.pub next to the signing key.
registry-guard:
	@test -n "$(REPO_WEB_URL)" || { echo "ERROR: no origin remote to derive the source links from; set REPO_WEB_URL=https://github.com/OWNER/REPO" >&2; exit 1; }
	@if [ -z "$(MIRROR_BASE)" ]; then echo "WARNING: MIRROR_BASE is empty: community plugins are built from the authors' repos only, with no source mirror fallback." >&2; fi
	@test -n "$(COMMUNITY_DIR)" || { echo "ERROR: set COMMUNITY_DIR to a fresh checkout of the plugins repo (git -C <clone> pull). Every plugin, first-party ones included, is built from its manifest there; without it the signed index would be empty." >&2; exit 1; }

# Fetch the archive already on the host (never overwrites a local file); run before `make registry`.
registry-pull-archive:
	@host=$$(DEPLOY_HOST="$(DEPLOY_HOST)" scripts/deploy-host.sh) || exit 1; \
	case "$(REMOTE_REGISTRY)" in /*[!A-Za-z0-9._/-]*|[!/]*) echo "ERROR: REMOTE_REGISTRY must be a plain absolute path" >&2; exit 1;; esac; \
	mkdir -p "$(ARCHIVE_DIR)" || exit 1; \
	ssh "$$host" "test -d '$(REMOTE_REGISTRY)/archive'"; rc=$$?; \
	if [ $$rc -eq 1 ]; then echo "registry-pull-archive: no archive on $$host yet, starting empty"; \
	elif [ $$rc -ne 0 ]; then echo "ERROR: cannot reach $$host (ssh exit $$rc)" >&2; exit 1; \
	else rsync -rt --ignore-existing --no-links "$$host:$(REMOTE_REGISTRY)/archive/" "$(ARCHIVE_DIR)/"; fi

registry: registry-guard
	@command -v jq >/dev/null || { echo "ERROR: jq is required (apt install jq / brew install jq)"; exit 1; }
	@test -n "$(HATAMOTO_REGISTRY_KEY_FILE)" || { echo "ERROR: set HATAMOTO_REGISTRY_KEY_FILE to the signing key file (see registry/README.md)"; exit 1; }
	rm -rf registry/packages registry/registry.json registry/plugins registry/meta.json registry/screenshots
	mkdir -p registry/packages registry/screenshots
	cd backend && go build -o ../registry/.hatamoto ./cmd/hatamoto
	printf '{}' > registry/meta.json
	env -u HATAMOTO_ALLOW_FILE_REPOS -u HATAMOTO_ALLOW_FILE_ARCHIVES -u HATAMOTO_SDK_REPLACE COMMUNITY_SKIP="$(COMMUNITY_SKIP)" FIRST_PARTY_OWNERS="$(FIRST_PARTY_OWNERS)" ARCHIVE_DIR="$(ARCHIVE_DIR)" HATAMOTO_MIRROR_BASE="$(MIRROR_BASE)" HATAMOTO_TRUST_MIRROR_VENDOR="$(TRUST_MIRROR_VENDOR)" HATAMOTO_BUILD_GOTOOLCHAIN="$(BUILD_GOTOOLCHAIN)" registry/build-community.sh "$(COMMUNITY_DIR)" registry/.hatamoto "$(HATAMOTO_REGISTRY_KEY_FILE)" registry/packages registry/meta.json || exit 1
	env -u HATAMOTO_ALLOW_FILE_REPOS -u HATAMOTO_ALLOW_FILE_ARCHIVES HATAMOTO_THEMES_SKIP="$(THEMES_SKIP)" registry/build-community-themes.sh "$(COMMUNITY_DIR)" registry/.hatamoto "$(HATAMOTO_REGISTRY_KEY_FILE)" registry/packages registry/meta.json registry/screenshots "$(ARCHIVE_DIR)" || exit 1
	registry/.hatamoto registry index registry/packages --meta registry/meta.json --key "$(HATAMOTO_REGISTRY_KEY_FILE)" --out registry/registry.json --url-prefix packages/
	@if [ "$(REGISTRY_PAGE)" = 1 ]; then \
	pub="$(HATAMOTO_REGISTRY_KEY_FILE:.key=.pub)"; trust=""; if [ "$$REGISTRY_PAGE_TRUST_SIGNER" = 1 ] && [ -f "$$pub" ]; then trust=$$(jq -r .public "$$pub") || exit 1; fi; \
	registry/.hatamoto registry page registry/registry.json --out registry/plugins/index.html $(REGISTRY_PAGE_ARGS) $${trust:+--trusted-key "$$trust"}; fi
	rm -f registry/.hatamoto

build-api:
	@echo "Building the hatamoto binary (linux)..."
	cd backend && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/hatamoto ./cmd/hatamoto
	@echo "Binary built: backend/bin/hatamoto"

build: build-api

# The four release artifacts (and their bundles, below), built the same way in CI (release.yml) and when
# rebuilding a release to compare sha256 (RUNBOOK 2.4). There are no defaults:
# RELEASE_VERSION and RELEASE_COMMIT must come from the environment or the
# command line, so a forgotten value stops the build instead of stamping
# `git describe` output. Every setting that changes the bytes is pinned here and
# not read from the caller's shell: GOAMD64, GOARM64, GOFLAGS, GOEXPERIMENT
# and the Go toolchain (the go.mod toolchain line). The values reach the shell
# as quoted environment variables, never pasted into the command text.
RELEASE_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
DIST ?= dist

release-build:
	@test -n "$$RELEASE_VERSION" -a -n "$$RELEASE_COMMIT" || { echo "set RELEASE_VERSION and RELEASE_COMMIT" >&2; exit 1; }
	rm -rf $(DIST) && mkdir -p $(DIST)
	dist=$$(cd $(DIST) && pwd); \
	tc=$$(awk '/^toolchain /{print $$2}' backend/go.mod); test -n "$$tc" || { echo "backend/go.mod has no toolchain line" >&2; exit 1; }; \
	for t in $(RELEASE_TARGETS); do os=$${t%/*}; arch=$${t#*/}; \
	  (cd backend && env GOENV=off GOWORK=off GOTOOLCHAIN="$$tc" GOAMD64=v1 GOARM64=v8.0 GOFLAGS= GOEXPERIMENT= \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -buildvcs=false \
	    -ldflags "-X hatamoto.dev/core/app.Version=$$RELEASE_VERSION -X hatamoto.dev/core/app.Commit=$$RELEASE_COMMIT" \
	    -o "$$dist/hatamoto-$$os-$$arch" ./cmd/hatamoto) || exit 1; done
	@$(MAKE) --no-print-directory release-bundles DIST="$(DIST)"

# One hatamoto-<os>-<arch>.tar.gz per target next to the bare binaries: the
# binary, LICENSE, NOTICE and THIRD_PARTY_LICENSES/ (the Go toolchain's licence,
# Go modules, the vendored JS, the OFL font licence; scripts/third-party-licenses.sh). The bare binaries stay: the signed record and the
# updater reference them, never the bundles. The tarballs are reproducible
# (sorted names, fixed mtime and owner, gzip -n) so a rebuild can compare them.
# Called by release-build; needs GNU tar.
release-bundles:
	@test -d "$(DIST)" || { echo "release-bundles is run by release-build" >&2; exit 1; }
	@tar --version | grep -q 'GNU tar' || { echo "release bundles need GNU tar" >&2; exit 1; }
	dist=$$(cd $(DIST) && pwd); stage=$$(mktemp -d); trap 'rm -rf "$$stage"' EXIT; \
	tc=$$(awk '/^toolchain /{print $$2}' backend/go.mod); test -n "$$tc" || { echo "backend/go.mod has no toolchain line" >&2; exit 1; }; \
	env GOENV=off GOWORK=off GOTOOLCHAIN="$$tc" GOFLAGS= scripts/third-party-licenses.sh "$$stage/tpl/THIRD_PARTY_LICENSES" || exit 1; \
	cp LICENSE NOTICE "$$stage/tpl/" && \
	for t in $(RELEASE_TARGETS); do n=hatamoto-$${t%/*}-$${t#*/}; \
	  rm -rf "$$stage/$$n" && cp -R "$$stage/tpl" "$$stage/$$n" && cp "$$dist/$$n" "$$stage/$$n/hatamoto" && \
	  chmod -R u=rwX,go=rX "$$stage/$$n" && \
	  tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C "$$stage" -cf - "$$n" | gzip -n -9 > "$$dist/$$n.tar.gz" || exit 1; done

# --- Deploy (hatamoto stack) ------------------------------------------------
need-host:
	@test -n "$(DEPLOY_HOST)" || { echo "ERROR: set DEPLOY_HOST (ssh host alias) or write it to .deploy-host" >&2; exit 1; }

# The binary migrates its own database on boot (`hatamoto serve`); ship.sh also
# runs `hatamoto migrate` with the new binary before the restart. The steps are
# separate targets so ship.sh can interleave the migration and roll back;
# `deploy` is all of them for a manual deploy.
upload-api: need-host build-api
	scp backend/bin/hatamoto $(DEPLOY_HOST):$(REMOTE_DIR)/bin/hatamoto.new
	ssh $(DEPLOY_HOST) "cd $(REMOTE_DIR)/bin && chmod +x hatamoto.new && mv -f hatamoto.new hatamoto"

# Installs deploy/systemd/hatamoto-api.service only when it differs; always
# daemon-reloads (cheap, idempotent). The box's User= and Group= are kept. It
# prints the diff and refuses (exit 1, nothing changed) when the box unit has
# drop-ins or Environment= lines the tracked unit lacks, unless FORCE_UNIT=1.
# check-unit runs the same checks without installing (ship.sh runs it before
# it mutates anything).
install-unit: need-host
	scp deploy/systemd/hatamoto-api.service $(DEPLOY_HOST):$(UNIT_PATH).new
	ssh $(DEPLOY_HOST) "UNIT=$(UNIT_PATH) FORCE_UNIT=$(FORCE_UNIT) sh -s" < deploy/install-unit.sh

check-unit: need-host
	scp deploy/systemd/hatamoto-api.service $(DEPLOY_HOST):$(UNIT_PATH).new
	ssh $(DEPLOY_HOST) "UNIT=$(UNIT_PATH) FORCE_UNIT=$(FORCE_UNIT) CHECK_ONLY=1 sh -s" < deploy/install-unit.sh

restart-api: need-host
	ssh $(DEPLOY_HOST) "systemctl restart hatamoto-api"

deploy: upload-api install-unit check-unit restart-api
	@echo "hatamoto.dev deployed and restarted"

# --- Migrations are embedded in the binary (backend/migrations, see its README).
migrate:
	@echo "Applying migrations to hatamoto_core (set HCORE_URL)..."
	@test -n "$(HCORE_URL)" || { echo "ERROR: set HCORE_URL=postgresql://user:pw@host:5432/hatamoto_core"; exit 1; }
	cd backend && DATABASE_URL="$(HCORE_URL)" go run ./cmd/hatamoto migrate

# --- Guards -----------------------------------------------------------------
# Neutrality guard (no site-specific names) and the Caddy splice test.
# vendor refreshes the third-party browser assets of the Go admin
# (backend/web/vendor, pinned by vendor.json). Network, Go only, no Node.
# Already-pinned bytes that changed upstream fail; add --update to accept:
#   cd backend && go run ./tools/vendor fetch --update
vendor:
	cd backend && go run ./tools/vendor fetch

# vendor-verify is the offline check: committed bytes match vendor.json.
vendor-verify:
	cd backend && go run ./tools/vendor verify

# vuln: known vulnerabilities in what the code calls, for the server, the SDK
# and the example plugins (network: fetches the vulnerability database).
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.9.0
vuln:
	cd backend && $(GOVULNCHECK) ./...
	cd backend/pkg/sdk && $(GOVULNCHECK) ./...

# licenses: scan every Go module in the build (fails on a non-permissive or
# unknown licence) and write the bundle for a release to build/licenses/
# (gitignored). The vendored JS licences are in backend/web/vendor/licenses/.
licenses:
	cd backend && go run ./tools/licenses -out ../build/licenses

# Admin template and CSS rules (docs/ADMIN_UI_RULES.md): static, no database.
ui-lint:
	cd backend && go test -count=1 -run 'TestUILint' ./internal/admin

# Computed-style checks in a real browser against a local instance
# (needs Chrome and Postgres: see scripts/admin-qa/run-local.sh).
ui-check:
	bash scripts/admin-qa/run-local.sh consistency.mjs consistency-families.mjs

# docs-publish: build and publish the documentation under docs/site to a site.
# Dry run by default (only reads); pass ARGS=--apply to write, ARGS=--lint to
# check the sources offline. Credentials come from HATAMOTO_URL and
# HATAMOTO_API_KEY or .docs-publish.env (read from the repository root, also
# for a relative --env-file), never from flags. See docs/site/README.md.
docs-publish:
	cd backend/tools/docs-publish && DOCS_PUBLISH_BASE_DIR="$(CURDIR)" go run . --root ../../../docs/site $(ARGS)

# The tool is its own Go module, so ./... in backend/ does not reach it.
docs-publish-test:
	cd backend/tools/docs-publish && go vet ./... && go test ./...

# budgets: deterministic size and allocation budgets (TestBudget* tests) against
# the committed baselines in backend/testdata/budgets/*.json. No timing, no DB.
# The recipe prints one table row per metric and exits non-zero on any failure.
budgets:
	@bash -o pipefail -c 'out=$$(cd backend && go test -count=1 -v -run "TestBudget" ./internal/admin ./internal/render ./internal/server ./internal/budgetfile 2>&1); rc=$$?; \
	  rows=$$(printf "%s\n" "$$out" | sed -n "s/^.*\(BUDGET [^ ]* .*\)$$/\1/p"); \
	  if command -v column >/dev/null 2>&1; then printf "%s\n" "$$rows" | column -t; else printf "%s\n" "$$rows"; fi; \
	  if [ $$rc -ne 0 ]; then printf "%s\n" "$$out" | grep -E "^(--- FAIL|FAIL|panic)|\.go:[0-9]+:" >&2; echo "make budgets: FAILED" >&2; fi; \
	  exit $$rc'

# budgets-update rewrites the baselines. It needs a reason, recorded in each
# file's history: make budgets-update REASON="B10 adds X"
# (REASON is read from the environment: REASON='say "x" $HOME' make budgets-update is safe;
# a REASON on the make command line has its own $ expanded by make)
budgets-update:
	@test -n "$$REASON" || { echo "set REASON=\"why the budgets change\"" >&2; exit 1; }
	cd backend && HM_BUDGETS_UPDATE=1 HM_BUDGETS_REASON="$$REASON" go test -count=1 -run TestBudget ./internal/admin ./internal/render ./internal/server

# check: ui-lint, then every suite in scripts/check-suites.txt (the pre-push hook runs
# only the suites whose paths changed: scripts/check-suites.sh --changed <range>).
check: need-staticcheck ui-lint
	bash scripts/check-suites.sh

# Local gates (CONTRIBUTING.md, "Local gates"): git hooks in .githooks. core.hooksPath is
# shared by every worktree of this clone, so it points into the git common dir, which does not
# move with a checkout: tiny dispatchers plus a SNAPSHOT of .githooks taken now. Re-run
# `make hooks` after pulling hook changes to refresh the snapshot.
hooks:
	@top=$$(git rev-parse --show-toplevel) && \
	  common=$$(cd "$$(git rev-parse --git-common-dir)" && pwd) && dir=$$common/hatamoto-hooks && \
	  { test -x "$$top/.githooks/pre-push" || { echo "make hooks: $$top has no .githooks/pre-push: check out a commit that has it" >&2; exit 1; }; } && \
	  rm -rf "$$dir" && mkdir -p "$$dir/snapshot/scripts" && \
	  cp -R "$$top/.githooks" "$$dir/snapshot/.githooks" && \
	  cp "$$top/scripts/check-neutral.sh" "$$top/scripts/check-suites.sh" "$$top/scripts/check-suites.txt" "$$top/scripts/affected-readers.txt" "$$dir/snapshot/scripts/" && cp "$$top/.gitleaks.toml" "$$dir/snapshot/" && \
	  for h in pre-commit commit-msg pre-push; do cp "$$top/.githooks/dispatch" "$$dir/$$h"; done && \
	  chmod +x "$$dir/pre-commit" "$$dir/commit-msg" "$$dir/pre-push" && \
	  git config core.hooksPath "$$dir" && \
	  echo "core.hooksPath = $$dir (all worktrees of this clone; snapshot of .githooks taken now)" && \
	  echo "hooks installed: pre-commit, commit-msg, pre-push (re-run make hooks after pulling hook changes; make hooks-off removes them)"

hooks-off:
	@common=$$(cd "$$(git rev-parse --git-common-dir)" && pwd) && rm -rf "$$common/hatamoto-hooks"; git config --unset core.hooksPath || true

# need-staticcheck: the pre-push hook and test-hooks run the pinned staticcheck;
# say so up front instead of failing deep inside them.
need-staticcheck:
	@sc=$$(command -v staticcheck || true); [ -n "$$sc" ] || sc=$$HOME/go/bin/staticcheck; \
	"$$sc" -version 2>/dev/null | grep -q '(0.8.2)' \
	|| { echo "staticcheck v0.8.2 is required: go install honnef.co/go/tools/cmd/staticcheck@v0.8.2" >&2; exit 1; }

test-hooks: need-staticcheck
	bash scripts/test-hooks.sh

help:
	@echo "Hatamoto Core targets: build build-api release-build deploy migrate check ui-lint ui-check hooks hooks-off budgets budgets-update vendor vendor-verify vuln licenses docs-publish docs-publish-test plugin-hello"
	@echo "Deploys target /opt/hatamoto + the hatamoto-api service only. Host: DEPLOY_HOST or .deploy-host."
