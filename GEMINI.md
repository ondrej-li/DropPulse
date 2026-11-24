# DropPulse — Project Instructions & Guidelines

## Overview & Architecture

DropPulse is a lightweight event and availability monitoring system organised as three elements in a single repository:

- **Backend (`backend/`)**: Written in Go (1.24+) using standard library `net/http`, `log/slog`, and PostgreSQL via `pgx/v5`. Provides the REST API, session management, OAuth/OIDC authentication (Google, Apple, Facebook), background event checking engine, email dispatch, and serves the single-page application.
- **Frontend (`frontend/`)**:
  - **App (`frontend/index.html`)**: Single-file web application with inline CSS and vanilla JavaScript. Served directly by the Go backend at `/` (single-user mode) or `/app/` (multi-user mode). Zero external build steps, dependencies, or package managers.
  - **Static Landing, Privacy & Terms (`frontend/public/`)**: Multilingual public landing pages (10 languages), privacy notices, and terms of service pages (Czech authoritative, English translation). Generated using `frontend/landing/build.py`.
- **Deployment & Config (`deploy/`)**: Nginx reverse proxy configurations, systemd service definition (`droppulse.service`), and deployment documentation.
- **Mobile (`mobile/`)**: Native iOS and Android app directory placeholders.

---

## Development & Test Workflows

### 1. Backend (Go)

- **Run unit and integration tests**:
  ```bash
  cd backend && go test ./...
  ```
- **Vet and static analysis**:
  ```bash
  cd backend && go vet ./...
  ```
- **Build backend binary**:
  ```bash
  cd backend && go build ./...
  ```
- **Cross-compile ARM64 binary (matching CI/production VPS)**:
  ```bash
  cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/droppulse-server ./cmd/server
  ```
- **Run locally (Single-user mode)**:
  ```bash
  createdb droppulse
  cd backend
  DATABASE_URL='postgres://localhost/droppulse?sslmode=disable' \
    SINGLE_USER_MODE=true \
    FRONTEND_FILE=../frontend/index.html \
    go run ./cmd/server
  ```

### 2. Static Landing Pages (Python)

- **Verify committed static pages are up to date**:
  ```bash
  python3 frontend/landing/build.py --check
  ```
- **Regenerate landing pages**:
  ```bash
  python3 frontend/landing/build.py
  ```

---

## Critical Serving & Security Invariants

### 1. Loopback-Only Binding
- The backend MUST bind loopback only: `BIND_ADDR=127.0.0.1`, `PORT=8090` (production) or `8080` (local). Never bind `0.0.0.0` or expose the Go service directly to the public network.
- Single-user mode strictly rejects startup if `BIND_ADDR` is not a loopback address.

### 2. Nginx Reverse Proxy
- All public traffic to `dp.ondralinek.cz` passes through nginx (`deploy/nginx/dp.conf`).
- Proxied locations are strictly `/app/` (exact) and `/api/` (prefix) to `http://127.0.0.1:8090`.
- `/healthz` must return `404` at nginx and is loopback-only for internal monitoring.
- Never add a generic reverse proxy wildcard rule.
- Keep `deploy/nginx/dp.conf` strictly in sync with the live vhost on the server.

### 3. Credential & Environment Protection
- Never commit secrets, OAuth client IDs/secrets, or private keys into git.
- Production environment variables reside outside the repository in `/home/agent/.config/droppulse/backend.env` (mode `0600`).

### 4. Anti-SSRF Enforcement
- The event checker (`backend/internal/watch/checker.go`) enforces strict IP restrictions: resolves hostnames once, rejects loopback, private, link-local, CGNAT, multicast, and reserved addresses, and dials the public IP directly to prevent DNS rebinding.

### 5. CAPTCHA Policy
- **No automated CAPTCHA solving**: Never implement or integrate CAPTCHA solving/bypass services. Only challenge detection, backing off, pausing, and notifying users with a manual link is allowed.

---

## Coding Conventions & Architectural Patterns

- **Go**:
  - Adhere to idiomatic Go: standard project layout (`cmd/`, `internal/`), `slog` for structured logging, `context.Context` propagation on all I/O boundaries.
  - SQL migrations are versioned sequentially in `backend/internal/db/migrations/*.sql` and applied at startup via `db.Migrate`.
  - Prefer composition and explicit dependencies injected via constructor functions (`New...`).
  - Wrap errors with `%w` for traceability.
- **Frontend SPA**:
  - Maintain `frontend/index.html` as a self-contained, dependency-free file (HTML + CSS + vanilla JS).
  - Use `SameSite=Lax` cookie and pass `X-CSRF-Token` headers for mutating requests (`POST`, `PUT`, `DELETE`).
- **Git Hygiene**:
  - Write concise, descriptive commit messages matching recent history (e.g., `feat: ...`, `fix: ...`, `docs: ...`).
  - Do not stage untracked scratch files or local configurations.

---

## Agent Operational Notes

- **Autopilot / YOLO Mode**: In YOLO mode, tool calls and file edits proceed without interactive confirmation prompts. Changes must remain surgical, test-backed, and strictly verified against project invariants.

