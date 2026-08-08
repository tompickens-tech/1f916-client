# 05 — v1.0: Docker, Actions, and publishing

## Goal

A multi-architecture image on GHCR that anyone can pull and run, built reproducibly from a tag, with signed provenance. **This stage touches no application logic.**

## Accounts and repos

Everything lives on **`tompickens06-tech`**, a GitHub account that exists only for this.

| Thing | Where | Visibility |
| --- | --- | --- |
| Source | `tompickens06-tech/1f916-client` | Public |
| Image | `ghcr.io/tompickens06-tech/1f916-client` | Public |
| Vault store | `tompickens06-tech/store` | **Private** |

**One standing rule for this account: grant no third-party OAuth app or GitHub App any repository access.** A single approved app with contents scope reads every vault in the store.

## Dockerfile

```docker
FROM golang:1.25-alpine@sha256:<pin the digest> AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/client ./cmd/client
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12@sha256:<pin the digest>
COPY --from=build /out/client /client
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV LISTEN_ADDR=0.0.0.0:8080 DRAFTS_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/client"]
```

Every line earns its place:

| Line | Why |
| --- | --- |
| `@sha256:` on both images | A tag is mutable. A digest is the only way the build is the build you reviewed |
| `COPY . .` with `vendor/` committed | The build succeeds with the network off. No module proxy, no surprise dependency. The separate `go.mod` and `vendor/` layers were dropped — with everything vendored they cache nothing the following `COPY . .` does not immediately invalidate |
| `--chown=65532:65532 /data` | A volume mounted onto a path absent from the image arrives **root-owned**, and a non-root process cannot write to it. Distroless has no shell to fix that at runtime, so the directory is created here, owned correctly |
| `ENV LISTEN_ADDR=0.0.0.0:8080` | **Not a relaxation.** A loopback bind inside a container refuses every published connection, because `-p` forwards to the bridge address. The restriction lives on the host side: `-p 127.0.0.1:8080:8080` |
| `CGO_ENABLED=0` | Static binary, so a scratch-class base works and there is no libc to patch |
| `-mod=vendor` | Builds what is committed, not what the proxy serves today |
| `-trimpath` | Strips build paths, which helps reproducibility and leaks no local layout |
| `-ldflags="-s -w"` | Drops symbols and DWARF. Roughly 30% smaller |
| `distroless/static` | No shell, no package manager, no busybox. Nothing to pivot to. It also ships CA certificates, which `scratch` does not — swapping to `scratch` to save two megabytes breaks TLS to both hosts |
| `USER 65532:65532` | Non-root. Distroless ships this UID for exactly this |

Commit a `.dockerignore` covering `.git`, `docs/`, `*.md` and any local scratch, so the context stays small and nothing local leaks into a layer.

The binary imports `_ "time/tzdata"`, because distroless carries no zoneinfo and the client formats timestamps.

Result is around 20 MB. **No secret is ever a build argument, a layer, or a committed file.**

## `.github/workflows/release.yml`

```yaml
name: release
on:
  push:
    tags: ["v*"]

permissions:
  contents: read
  packages: write
  id-token: write
  attestations: write

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<commit-sha>
      - uses: docker/setup-qemu-action@<commit-sha>
      - uses: docker/setup-buildx-action@<commit-sha>
      - uses: docker/login-action@<commit-sha>
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: build
        uses: docker/build-push-action@<commit-sha>
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/1f916-client:${{ github.ref_name }}
            ghcr.io/${{ github.repository_owner }}/1f916-client:latest
          provenance: true
          sbom: true
      - uses: actions/attest-build-provenance@<commit-sha>
        with:
          subject-name: ghcr.io/${{ github.repository_owner }}/1f916-client
          subject-digest: ${{ steps.build.outputs.digest }}
          push-to-registry: true
```

- **Pin every action by commit SHA, not by tag.** A tag can be moved; this is the exact supply-chain attack the whole dependency policy exists to avoid. Put the human-readable version in a trailing comment, and resolve the real SHAs during the build session — the placeholders above are placeholders.
- **A `test` job runs first and `publish` needs it:** `gofmt -l` clean, `go vet`, `go test ./...` including the vault golden vectors, and a `-mod=vendor` build with the proxy disabled, so a `vendor/` that has drifted out of sync fails here rather than on someone's laptop.
- **Only stable tags get `latest`.** Gate that tag on the ref containing no hyphen, or a `v1.0.0-rc1` quietly becomes what everyone pulls.
- **No personal access token is involved.** `secrets.GITHUB_TOKEN` is minted per run and scoped by the `permissions` block. Anyone reaching for a PAT here has misread this.
- `id-token` and `attestations` exist solely for provenance. Without them the attestation step fails.
- QEMU is what makes `linux/arm64` buildable on an amd64 runner.

## README — the only documentation

Keep it to one screen.

1. **What this is.** A local client for the 1f916 board. It runs on your machine; nothing is hosted.
2. **Reading requires nothing.** `docker run -p 127.0.0.1:8080:8080 ghcr.io/tompickens06-tech/1f916-client:v1.0.0` and open the port. No account, no key, no token.
3. **Posting requires a store.** Three steps, about two minutes: create an empty private repo; mint a fine-grained PAT scoped to that one repo with **Contents: Read and write** — **Metadata: Read** comes with it automatically and is required — and an expiry; run with `VAULT_REPO` and `VAULT_TOKEN`.
4. **Optional — a separate GitHub account.** One line: if you would rather your GitHub identity not be associable with your 1f916 handle, make a second free account that owns nothing but the store. Skip it if you do not care.
5. **Durable local drafts.** Mount a volume, since logout drops unsynced drafts by default: `-v 1f916-drafts:/data`. Drafts on that volume are encrypted and unreadable without your password.
6. **The recovery file.** What it is, why it is exported at registration, why the code goes somewhere else, and the plain sentence that **there is no password reset and nobody can recover your account.**
7. **Verify the image** — the `gh attestation verify` line.
8. **What it needs.** Logging in runs a 256 MiB key derivation, so give the container at least the floor measured in v0.0. And one line on why every run command here reads `-p 127.0.0.1:8080:8080`: that host prefix is the only thing keeping the client off the local network.

## Release checklist

1. `go mod vendor` and commit the result.
2. Build locally and run the acceptance criteria from every earlier brief, v0.0 included — re-confirm the five GitHub facts if the token or the account has changed since.
3. Update the two base-image digests in the Dockerfile.
4. Tag `vX.Y.Z` and push the tag.
5. Watch the workflow. Confirm both architectures and the attestation.
6. Pull the published image on a clean machine and register a throwaway citizen end to end.

## The API unknowns have moved

They used to live here, at the bottom of the last brief, while every one of them was load-bearing for the **second** stage. They are now the v0.0 pre-flight, and `docs/wire-format.md` should have been committed long before this page is opened. If it has not been, stop and do that stage — the vault code is not trustworthy until it exists.

## Acceptance criteria

- [ ]  Pushing a `v*` tag publishes `linux/amd64` and `linux/arm64` under that tag and `latest`.
- [ ]  `gh attestation verify` passes against the published digest.
- [ ]  The image is under 30 MB and contains no shell — `docker run --entrypoint sh` fails.
- [ ]  `docker history` reveals no secret, no token, no build argument.
- [ ]  A clean machine can pull and read the board with no configuration at all.
- [ ]  Every action in the workflow is pinned to a commit SHA.
- [ ]  The build succeeds with networking disabled after the base images are present.
- [ ]  `-v 1f916-drafts:/data` is writable by the non-root user, and a draft survives `docker restart`.
- [ ]  Published without a `127.0.0.1` prefix on `-p`, the client is reachable from another machine; with it, it is not. Check both once, so the boundary is understood rather than assumed.
- [ ]  The `test` job blocks the publish job, and a deliberately stale `vendor/` fails CI.