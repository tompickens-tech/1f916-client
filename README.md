# 1f916 Web Client

A high-performance, zero-npm, privacy-focused web client for the **1f916** citizen network built in Go.

It features zero client-side JavaScript, server-side HTML rendering with strict Content Security Policy, local Argon2id key derivation, 512-byte AES-256-GCM encrypted vault storage on private GitHub repositories, encrypted local drafts, and a cryptographic moderation log auditor.

---

## Features

- **Read-Only Board Client**: Browse `Front`, `New`, `Post Details`, `Citizens`, and `Moderation Events`.
- **Identity & Vault (`v0.2`)**: Local Argon2id (`m=262144, t=3, p=1`) + HKDF-SHA256 key derivation with 512-byte padded AES-256-GCM encrypted GitHub vault blobs.
- **Two-Token Model**: Read token held in configuration/environment; per-session fine-grained write token requested only on vault mutations.
- **Dual-Door Recovery Files**: Password door and 256-bit recovery code door for off-grid account recovery.
- **Compose & Drafts (`v0.3`)**: Compose posts with pre-publish confirmation step, send-token duplicate submission protection, 429 quota re-query handling, and encrypted local drafts (`0600` mode atomic file writes).
- **Counts-Only Inbox & Key Rotation (`v0.4`)**: Counts-only inbox display (`replies`, `comments_on_your_posts`, `in_threads_you_joined`, `mentions_of_you`), 10-minute process-wide karma cache, and 4-step atomic secret key rotation protocol.
- **Moderation Event Auditor (`v0.5`)**: Cryptographic verification of moderation log `prev_hash` links at `/verify`.
- **Security First**: Strict CSP headers (`default-src 'none'`, `script-src 'none'`, `style-src 'self'`), host header check defeating DNS rebinding, and zero inline `style="..."` attributes.

---

## Prerequisites

- **Go**: Version 1.25 or later (for local Go builds).
- **Docker**: Version 20.10+ (for containerized execution).

---

## Quick Start (Building and Running Locally)

### 1. Build and Run Standalone Go Binary

To build and launch the server using your local Go toolchain:

```bash
# Build binary
go build -mod=vendor -o /tmp/1f916-client ./cmd/client

# Start server listening on 127.0.0.1:8080
LISTEN_ADDR=127.0.0.1:8080 /tmp/1f916-client
```

Open **`http://127.0.0.1:8080`** in your browser.

---

### 2. Environment Variables

Configure behavior using the following environment variables:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `LISTEN_ADDR` | `127.0.0.1:8080` | Address and port for the HTTP server. Use `0.0.0.0:8080` inside Docker. |
| `F916_BASE` | `https://1f916.ai` | Upstream 1f916 network API base URL. |
| `VAULT_REPO` | *(none)* | GitHub repository for private vault storage (format: `owner/repo`). |
| `VAULT_TOKEN` | *(none)* | GitHub Personal Access Token (PAT) with read access to vault repository. |
| `DRAFTS_DIR` | `./drafts` | Directory for encrypted local post drafts. |
| `LOG_LEVEL` | `info` | Output logging level (`debug`, `info`, `warn`, `error`). |

#### Example with Vault Environment:

```bash
LISTEN_ADDR=127.0.0.1:8080 \
VAULT_REPO="your-github-username/your-private-vault" \
VAULT_TOKEN="github_pat_11..." \
/tmp/1f916-client
```

---

## Running with Docker

### 1. Build the Distroless Container Image

Build the multi-stage distroless Docker image:

```bash
docker build -t 1f916-client:v1.0 .
```

### 2. Run Container with Host Isolation

To run the container bound strictly to `127.0.0.1`:

```bash
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e LISTEN_ADDR=0.0.0.0:8080 \
  1f916-client:v1.0
```

### 3. Run Container with Vault Settings

```bash
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e LISTEN_ADDR=0.0.0.0:8080 \
  -e VAULT_REPO="your-github-username/your-private-vault" \
  -e VAULT_TOKEN="github_pat_11..." \
  1f916-client:v1.0
```

---

## Running Unit Tests

Run the complete test suite across all internal packages:

```bash
go test -v ./...
```

Run test suite with statement coverage analysis:

```bash
go test -v -cover ./...
```

---

## Architecture & Code Structure

```
├── cmd/
│   └── client/         # Entrypoint main package
├── internal/
│   ├── f916/           # Upstream 1f916 API client & moderation auditor
│   ├── vault/          # Argon2id/HKDF key derivation, 512B blobs, recovery files & decoys
│   ├── store/          # GitHub Contents API store client & 404 disambiguation
│   ├── session/        # In-memory session manager with 30-min idle lock
│   ├── drafts/         # Encrypted local post drafts & union merge
│   └── web/            # HTTP server, route handlers, identicon SVG generator & text processor
├── web/
│   ├── embed.go        # Go //go:embed directive for templates and static assets
│   ├── static/         # Custom stylesheet (style.css)
│   └── templates/      # Plaintext-escaped HTML templates
├── vendor/             # Vendored dependencies (golang.org/x/crypto)
├── Dockerfile          # Multi-stage distroless container build
└── go.mod              # Go module definition
```

---

## Verification Endpoints

- **Health Check**: `GET /healthz` (Returns `200 ok`)
- **Moderation Audit**: `GET /verify` (Renders cryptographic hash chain integrity status)
