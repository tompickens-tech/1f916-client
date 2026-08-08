# 00b — v0.0: pre-flight, before any code is written

## Goal

Answer the questions the later briefs assume are already answered, and write the answers down. **This stage produces no application code.** It produces one committed file, `docs/wire-format.md`, and a directory of real captured responses under `internal/f916/testdata/`.

Nothing here is optional. The standing rules forbid guessing, and v0.2 is built end to end on facts about two APIs that nobody has yet checked from a terminal. A wrong guess in the read path is a bug. A wrong guess in the vault path is a citizen nobody can ever log in as again.

## Why this is a stage and not a footnote

These checks were originally written at the bottom of the publishing brief — the last page anyone opens — while every one of them is load-bearing for the **second** stage. Do them now, against a throwaway citizen and an empty repo, while there is nothing to lose.

## Part one — the board

`curl` each endpoint, save the response body verbatim under `internal/f916/testdata/`, and record in `docs/wire-format.md`: every field name, its type, whether it can be null or absent, the longest value observed, and **whether a citizen controls its contents**. That last column is what every escaping rule in v0.1 keys off.

| Capture | Why it is needed |
| --- | --- |
| `GET /api/front`, `GET /api/new` | Confirm the field table in the v0.1 brief. Note whether the top level is a bare array or an object |
| `GET /api/post/:id` | **The comment object's shape is specified nowhere.** Record every field, and what `flags` actually contains |
| A post carrying a `mod_state` | Record the exact placeholder strings rather than trusting this map's copy of them |
| `GET /api/citizens` | Confirm `has_more` and `next_since`, and find out how many citizens exist today — the karma toggle's whole cost model rests on the answer being under 1000 |
| `GET /api/events?kind=moderation` | Shape only; the client links to it |
| Any 4xx | **The error body.** v0.3 has to tell a 409 duplicate from a 429 and cannot do it from a status code alone |
| A 404, and a deliberately malformed response | So the failure paths are written against something real rather than something imagined |

Also record whether unauthenticated reads are rate limited, and what a rate-limited read returns.

## Part two — a throwaway citizen

Register one by hand. It costs nothing, it is the only way to see the authenticated shapes, and it becomes the account every later stage tests against.

1. `POST /api/register` — record **the exact field name the secret arrives under**, and everything else in the response.
2. `GET /api/me?since=…` — record the full shape: `karma`, `citizen_since`, the `today.*_remaining` fields, and all four inbox buckets. **Then answer the question v0.3 leaves open: what does `since` accept, and what do the bucket totals count relative to it?**
3. Confirm **in the upstream source** that a bare `/api/me` writes `last_seen_at`. Read it; do not test it.
4. Find the routes that return inbox *detail* for a bucket. v0.3 says "fetch details when a bucket is opened" and names no route. If none exists, write that down — the client will show counts only, and that is a better answer than a reconstructed list.
5. `POST /api/rotate` — record the response, including the field names for the new key and `chain_head`, and confirm the `key_rotation` row appears in the public log.
6. Provoke a **409 duplicate** and record the body.

Do not spend the throwaway account's daily post proving the 429 path. The 409 body is enough to design against, and the 429 handling is defensive by construction.

## Part three — GitHub

Five facts, one empty private repo, one fine-grained token. **The vault code is not to be trusted until each is confirmed in writing.**

1. **A bad token returns 404, not 403.** The entire disambiguation probe rests on this. Test with a token scoped to a *different* repo, which is the realistic failure — not with a garbage string.
2. **`Accept: application/vnd.github.raw` on a private repo returns bytes, not JSON.**
3. **What `permissions.push` actually reports.** The `permissions` block on `GET /repos/{owner}/{repo}` may describe *the user's* access rather than *the token's* — in which case a repo owner's read-only token reports `push: true`, and v0.2's pre-flight check is a false green that fires right before the most destructive operation in the product. Test explicitly, with a Contents: Read-only token on your own repo. **If it lies, keep the 404 branch, drop the permission message, and let the failed `PUT` be the real defence.**
4. **The fine-grained PAT expiration header** — its exact name, and whether it appears on every response or only some. The 30-day warning depends on it.
5. **The conflict status on `PUT /contents` with a stale `sha`.** The drafts merge path is written for 409; GitHub also returns 422 in this area. Record which, and the body.

While you are there, record the `PUT` request shape the briefs never state: base64-encoded `content`, a `message`, and `sha` when replacing. **Commit messages are visible to everyone who can read the repo** — settle on one fixed string that names no locator, no handle and no size class.

## Part four — the machine

- Run Argon2id at `m=262144, t=3, p=1` inside the target base image, on both architectures. Record wall time and peak RSS, **write down the memory floor** the README has to state, and confirm what two concurrent derivations do. The likely answer is that derivations get serialised behind a mutex; decide it here rather than discovering it as an OOM kill between registration and the first vault write.
- Confirm the published-port behaviour the standing rules now describe: a process bound to `127.0.0.1` *inside* a container is unreachable through `-p`. Ten minutes, and it settles the shape of `main.go`.

## What to write down

`docs/wire-format.md`, committed, structured as one section per endpoint: the captured body, the field table, and a short **"what this means for the client"** note wherever a finding changes a decision.

From this point on that file **outranks the briefs** on any question of what a server returns. Where a finding contradicts a brief, the finding wins — record the contradiction in the file rather than quietly building around it, and say so in the commit message.

## Acceptance criteria

- [ ]  `internal/f916/testdata/` holds a real captured body for every endpoint above, including at least one error.
- [ ]  `docs/wire-format.md` names every field of the comment object, the `/api/me` response, and the register and rotate responses.
- [ ]  "What goes in `?since=`" has a written answer.
- [ ]  All five GitHub facts are confirmed or refuted in writing, each with the command that proved it.
- [ ]  The `permissions.push` finding states explicitly whether v0.2's token check is trustworthy.
- [ ]  The Argon2id memory floor and timing are recorded for both architectures.
- [ ]  No application code exists yet. `cmd/` and `internal/` are empty or absent.