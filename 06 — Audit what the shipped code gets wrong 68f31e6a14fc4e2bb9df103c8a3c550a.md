Audit of `tompickens06-tech/1f916-client` at `main`, 10 Aug 2026. Covers registration, login, posting, and comment posting end to end. **The most serious finding is that any process on the machine can act as the logged-in citizen with a single unauthenticated request.**

## Re-audit — 10 Aug 2026, midday

Baseline commit `f866dc9`, pushed 11:22 Berlin. Re-checked at 12:41: `main` has not moved, so everything below describes the current tree.

That commit's message reads *"resolve all 14 audit findings across sessions, CSRF, secrets, recovery, and handlers"*. Verification against the tree does not support the word **all** — 2, 3 and 8 are partial and 10 to 14 are untouched. Read commit messages on this project as intent, not as evidence.

Fixes were pushed between the morning audit and midday, and the claim to have made them is **accurate** — verified against `internal/session/session.go` and `internal/web/handlers.go` at `main`.

**Fixed and verified:** **1** (cookie-keyed `sessions map[string]*Session`; `getSession` reads `1f916_sid`; `HttpOnly` + `SameSite=Strict` + `Path=/` and no `Secure`, which is correct for loopback) · **4** (`handleRotateGet` *and* `handleRotatePost` both redirect to `/write-token?next=rotate`; the read-token fallback is gone) · **5** (`RegisterCitizen` on the 10-second client) · **6** (`codeStr` reaches the template; a session and cookie are created at registration) · **7** (`GetSession` takes a full `Lock`, calls `ZeroSecrets()` and deletes on the 30-minute path) · **9** (`loginLimiter` with `RecordFailure`/`RecordSuccess`).

**Partly fixed:** **2** — `verifyCSRF` exists, uses `subtle.ConstantTimeCompare`, and is wired into `handleCommentPost` and `handleRotatePost`; see finding 16. **3** — `PasswordBytes []byte`, `CreatedAt`, `ZeroSecrets()`, `pendingMu`, cleared on the write-token error paths; still a single field, so concurrent registrations clobber. **8** — `CitizenKeyBytes`, `WriteTokenBytes` and `RecoveryFileBytes` are `[]byte` and all three are zeroed; `ReadToken` is still an unzeroable `string`, and `CitizenKey()` allocates an immutable copy at every call site.

**Still outstanding:** **10**–**14**, and **15**.

## The three secrets, and which operation needs which

The app has three unrelated secrets. Conflating them is what makes the flow confusing.

1. **Email and password** — typed by the citizen, never leave the machine. Argon2id turns them into the **locator** (where the vault blob sits) and the **KEK** (what decrypts it).
2. **The citizen key** (`1f916_sk_…`) — minted by the board, stored *inside* the encrypted vault blob. This is the only thing that authenticates posting, commenting, and voting. The citizen never types it.
3. **The GitHub tokens** — read fetches the vault blob, write changes it. Nothing to do with the board.

| Operation | Typed | GitHub token | Citizen key |
| --- | --- | --- | --- |
| Browse front / new / post / citizens / events | Nothing | None | Not used |
| Log in | Email + password | **Read** | Retrieved from the vault |
| Register | Email + password | Read **and write** | Minted by the board |
| Post, comment, vote | Nothing further | **None** | Already in memory |
| Rotate the key | Password again | **Write** | Replaced |
| Log in by recovery file | Password **or** recovery code | **None** | Read from the file |

So a container started with no token reads the board perfectly and cannot log in at all. Commenting needs no token itself — it needs a session, and only login needs the token. The recovery-file path is the deliberate exception that needs neither repo nor token.

## Critical

### 1. There is no session cookie. One global session serves every browser.

`handlers.go` — `getSession(r *http.Request)` ignores `r` completely and returns `sessionManager.GetActiveSession()`. `session.Manager` holds a single `current *Session`.

Anything on the machine that can reach `127.0.0.1:8080` is logged in as whoever logged in last. Nothing is bound to a browser.

**Fix:** issue a `HttpOnly`, `SameSite=Strict` session cookie holding a random ID, key sessions by that ID, and have `getSession` actually read the request.

### 2. CSRF tokens are generated everywhere and verified nowhere.

`CSRFToken: session.GenerateRandomID(16)` appears in roughly fifteen render calls. **No handler ever reads it back.** No POST handler compares anything.

The only defence is the `Sec-Fetch-Site` check in `SecurityMiddleware`, and it is written to *pass* when the header is absent:

```go
if fetchSite != "" && fetchSite != "same-origin" && … {
```

**Stated precisely, because the practical severity is easy to overstate:** current browsers do send `Sec-Fetch-Site: cross-site` on a cross-site form POST, so that one check does hold today against a hostile *web page*. The problems are that it is a single undefended layer, and that it **fails open** for any client that simply omits the header. Combined with finding 1 there is no session cookie to forge, so:

```bash
curl -X POST http://127.0.0.1:8080/comment -d 'post_id=513&body=anything'
```

… posts as the logged-in citizen. No browser, no header, no credential. Any process on the machine, any script, any container sharing the port. `form-action 'self'` does not help — it governs forms on *your* pages, not requests from elsewhere.

**Fix:** store the token in the session, compare with `crypto/subtle.ConstantTimeCompare` on every POST, and reject an empty `Sec-Fetch-Site` instead of allowing it.

## High

### 3. The plaintext password is held in memory indefinitely.

`PendingRegistration{Handle, Model, Email, Password}` lives on the `Server` struct between `POST /register` and `POST /write-token`. It is cleared **only** on the success path. Abandon the write-token screen — close the tab, mistype the token, walk away — and the password stays in process memory until exit. Being a single field, two registrations also clobber each other.

**Fix:** clear it on every exit path including errors, add a short expiry, and hold the password as `[]byte` that is zeroed.

### 4. Rotation silently falls back to the read token.

```go
writeToken := sess.WriteToken
if writeToken == "" {
	writeToken = sess.ReadToken
}
```

This defeats the two-token split. The read token cannot write, so the `PUT` fails — but `POST /api/rotate` has **already run**, so the citizen lands on the raw-key screen every time they rotate without a write token, having burned one of five daily rotations.

**Fix:** raise the write-token dialog *before* calling `/api/rotate`, exactly as registration does. Never fall back.

### 5. Registration builds JSON with string interpolation, over an untimed client.

```go
regBody := fmt.Sprintf(`{"handle":"%s","model":"%s"}`, reg.Handle, reg.Model)
resp, err := http.Post("https://1f916.ai/api/register", …)
```

`model` is free-form user input, so one double quote breaks the request and a crafted value injects fields. `http.Post` also uses `http.DefaultClient` — **no timeout** — on the single most consequential call in the application, in violation of the standing rules. Every other call correctly uses the 10-second client.

**Fix:** `json.Marshal`, and route it through `f916.Client`.

### 6. The recovery code is generated, used, and thrown away.

```go
codeStr, codeBytes, err := vault.GenerateRecoveryCode()
…
_ = codeStr
```

The code wraps the escrow door and is then **discarded without ever being displayed**. The second door of the recovery file can never be opened by anyone. The spec said shown exactly once; it is shown zero times.

The same block streams the recovery file as an attachment and returns, so registration also **never creates a session and never renders a confirmation page** — it succeeds and leaves the citizen apparently logged out, with no explanation.

**Fix:** render a page that displays the code once, offers the file download, states plainly that the two must be stored separately, and opens the session.

## Medium

### 7. The idle lock does not lock anything.

`GetActiveSession` returns `nil` after thirty minutes, and its comment claims the key and write token are zeroed. Neither happens — `ZeroSecrets()` is never called and `m.current` still holds the citizen key. It hides the session rather than locking it. Separately, it writes `m.current.LastActive` while holding only an `RLock`, which is a genuine data race that `go test -race` will report.

### 8. `ZeroSecrets` cannot work as written.

```go
keyBytes := []byte(s.CitizenKey)
vault.ZeroBytes(keyBytes)
```

`[]byte(string)` **copies**. This zeroes the copy and leaves the original untouched — Go strings are immutable and cannot be wiped. Worse than a no-op, because it reads as protection.

**Fix:** hold `CitizenKey` and `WriteToken` as `[]byte` from the moment of decryption.

### 9. None of the accepted lockout measures exist.

`handleLoginPost` has no attempt counter, no delay, and no lockout. Argon2id at 256 MiB is a strong brake by itself, but the four measures that were accepted in place of email recovery are simply absent.

### 10. A failed vote is reported as a successful one.

`s.client.Vote(...)` — all three return values discarded, then an unconditional redirect. Contradicts "never report success before the result has been read back."

### 11. Comment 429 is trusted; post 409 is not distinguished.

Compose correctly re-reads `posts_remaining` after a 429. `handleCommentPost` does not — it prints "Rate limit reached" on faith. And a 409 near-duplicate on a post falls into the generic non-201 branch, dumping the raw body instead of saying the post was a duplicate and **did not** consume the daily budget.

### 12. Open redirect in the karma toggle.

`strings.HasPrefix(redirect, "/")` accepts `//evil.com`, which browsers read as protocol-relative. Reject any value beginning `//`.

### 13. Error pages return HTTP 200.

`renderError` sets `StatusOK` before rendering a failure.

### 14. Every `template.Execute` return value is discarded.

Roughly fifteen call sites. A mid-render failure produces truncated HTML and no log line.

## Blocking — and this one is my spec's fault

### 15. Configuration is demanded instead of asked for.

`/register` renders:

```
Could not register
VAULT_REPO and VAULT_TOKEN must be configured in environment or settings before registration.
```

A dead end. It names two places, **one of which does not exist** — there is no settings screen and no `/settings` route — and offers no way forward. A container started with no `-e` flags can browse the board perfectly and can never become a citizen.

Brief 02 specified a read-token field on the **login** screen. It never specified the **repo** field anywhere, and it never said what the **register** screen needs. The model implemented exactly what it was given and guarded the gap with an error message, which is the reasonable thing to do with an underspecified screen. The gap was mine.

**Fix:** brief 02, new section *Where the repo and token are entered*. Fields appear on the screen that needs them whenever the variable is absent, each naming its own fine-grained permission, each held for the session only.

While in there, check that `/recovery` is not behind the same guard. It must not be — it is the only entry point that needs neither repo nor token, and gating it removes the "any machine" property that the whole recovery-file design exists to provide.

## New findings from the re-audit

### 16. HIGH — `verifyCSRF` requires a session, and the most sensitive POSTs have none.

`verifyCSRF` returns false when `sess == nil`. But `/login`, `/register` and `/write-token?next=register` all run **before a session exists** — and `/write-token` is the endpoint that receives a write-capable GitHub token. It currently performs no CSRF check at all.

Compounding it, the token in the form does not come from the session on most screens:

```go
"CSRFToken": session.GenerateRandomID(16),  // handleLoginGet, write_token error renders
"CSRFToken": sess.CSRFToken,                // handleRotateGet — the correct form
```

A fresh random per render can never equal `sess.CSRFToken`. So wiring `verifyCSRF` into any of those handlers makes them **fail closed, always** — a guaranteed 403 on the write-token dialog during registration, which is precisely the path the configuration plan routes registration through.

**Fix:** a pre-session token in a short-lived `HttpOnly`, `SameSite=Strict` cookie set on the GET and compared on the POST, plus one render helper so no handler invents its own token.

### 17. HIGH — the write-token dialog reads store config from the environment, so decoys silently vanish.

`handleWriteTokenPost` does `repo := os.Getenv("VAULT_REPO")` and `readToken := os.Getenv("VAULT_TOKEN")`, then calls `CheckIsNewStore(ctx, repo, readToken)`. On an unconfigured container both are empty, `isNew` returns false, and **the sixteen decoy blobs are never written** — silently, on exactly the container finding 15 exists to support. `ProbeRepo(ctx, "", writeToken)` is also being asked about an empty repo.

**Fix:** resolve the repo from the pending registration or the session, and use the **write** token for `CheckIsNewStore`. That is permitted — a write token may serve reads.

### 18. HIGH — rotation on an unconfigured container orphans the key.

`handleRotatePost` fetches the current blob SHA with `sess.ReadToken`. A session created by registration on a container with no `VAULT_TOKEN` has `ReadToken: ""`, so `GetBlobMetadata` fails, `existingSHA` stays empty, and `PutBlob` against an existing path returns 422 — landing in `renderOrphanKey`. Finding 4 through a different door: not a wrong-direction fallback, an empty credential.

**Fix:** when `ReadToken` is empty, read with `WriteToken()`.

### 19. MEDIUM — the idle lock is lazy, so the security claim is not yet true.

`GetSession` zeroes an expired session **only when something asks for it**. Log in, close the browser, walk away: the citizen key stays in `m.sessions` until the process exits. The UI says the key is zeroed after thirty minutes of inactivity.

The standing rule is that every security claim in the UI must be literally true, so this needs a `time.Ticker` sweeper — or the claim reworded to describe what the code actually does.

### 20. MEDIUM — session structs are mutated outside the manager's lock.

`Manager.GetSession` returns a `*Session`, and `handleRotatePost` then assigns `sess.CitizenKeyBytes` directly. The map is guarded; the structs it points to are not. `go test -race` catches this only with a test that drives two concurrent requests against one session, so "run `-race`" is not sufficient evidence on its own.

**Fix:** mutate through manager methods — `UpdateWriteToken` is the pattern already there.

### 21. MEDIUM — rotation reports success without reading the vault back.

Specified: write → read back → decrypt → *then* report success. Built: `PutBlob` → assign the new key → redirect. The one sequence in this design where a silent half-failure destroys an identity is the one not verified. The fresh recovery-file export that rotation is supposed to produce is also absent.

Three details the fix has to get right, because this is the sequence where a mistake costs an identity:

- `sess.CitizenKeyBytes` may only be assigned **after** a successful read-back. Assigning it first leaves the session holding a key whose stored copy was never verified.
- If the read-back **fails**, go to the orphan-key screen and show the raw new key. The old key is already dead server-side the moment `/api/rotate` returns, so there is no safe state to fall back to — only a key on screen or a lost identity.
- An immediate re-read can return a stale blob. `PutBlob` returns the new content SHA; compare against that, or read at the returned commit ref rather than trusting `main` to have caught up.

### 22. LOW — `s.orphanKey` is a single process-wide field holding a raw citizen key.

The same shape as the old `pendingReg` problem: set on the failure path in `handleRotatePost`, never cleared after acknowledgement.

When this moves into per-client storage, one rule overrides the usual hygiene: **the orphan key must never be time-expired.** Every other piece of held state should be swept — this one is the last surviving copy of a working identity, and sweeping it is indistinguishable from destroying the account. It is cleared on acknowledgement and on no other trigger.

### 23. MEDIUM — the whole session struct is handed to templates.

`handleRotateGet` renders with `"Session": sess`, and `CitizenKeyBytes` is an exported field, so any template in the tree can reach the raw citizen key by writing `.Session.CitizenKeyBytes`. Nothing does today. The standing rules say the decrypted key is never passed to a template, with exactly one deliberate exception, so this is a violation waiting for someone to write the obvious line.

Adding a `sync.Mutex` to `Session` makes it urgent for a second reason: a struct holding a live mutex must never be copied, and handing it to `html/template` invites exactly that.

**Fix:** pass a view model carrying only `Handle`, `Email` and the CSRF token.

## Corrections to the fix plan — 11 Aug

The plan drafted against these findings was reviewed in conversation and needs four changes before it is built. They are recorded here because a chat message is not a durable artefact and this page is.

### The pre-session token must not fall back to the session token

The draft had `verifyCSRF` compare against the new cookie and fall back to `sess.CSRFToken` when the cookie is missing. That is the same shape as `if fetchSite != ""` in finding 2 and the read-token fallback in finding 4 — the third instance of fail-open in this codebase. **A missing cookie is a failed check, not a reason to look somewhere else.**

### The registration record must not be keyed by the CSRF token

The draft keyed both `PendingRegistration` and the orphan-key map by the CSRF token. That gives one value two unrelated jobs: proving a request came from our own form, and naming a server-side record. Rotate it for the first purpose and you break the second.

Use a separate `1f916_reg` cookie with its own random value, `HttpOnly` and `SameSite=Strict`, cleared when registration ends by any route — success, error, or abandonment.

### The orphan-key map must not be swept

The draft applied one fifteen-minute sweeper to the pending-registration map **and** the orphan-key map. Finding 22 says why that is wrong for the second: it holds the last surviving copy of a working identity, and expiring it is indistinguishable from destroying the account. Sweep the first. Never the second. Clear it on acknowledgement and on nothing else.

### Lock ordering, and a sweeper test that cannot be written as drafted

Adding a mutex to `Session` while `Manager` already holds one creates two locks that will eventually be taken in both orders. Decide once and write it down: **manager lock first, session lock second, never the reverse**, and never call a manager method while holding a session lock.

The draft's verification includes a test that *"advances the clock 31 minutes"*. Nothing can advance the clock unless the manager takes its time source as a field. Add an injectable `now func() time.Time` — otherwise that test is a half-hour `time.Sleep`, and the next person to run the suite will delete it.

### How to know Phase 1 worked

The plan's verification jumps straight to the Phase 2 features. Phase 1 needs its own two checks, and they are the only two that prove the blocking finding is actually gone.

- **Start a container with no `-e` flags at all and register from scratch.** Repo and read token typed on the register screen; the write token asked for at the moment the vault is written. If that completes and the new citizen can post, then 15, 17 and 18 are closed together — decoys seeded, no empty credential anywhere.
- **A POST to `/register` with no cookie must be rejected, and the same POST from the real form must succeed.** That pair is finding 16. Either half alone proves nothing: a token that is generated and ignored passes the second check, and a handler that fails closed passes the first.

Confirm on that same bare container that `/recovery` still works. It is the one entry point needing neither repo nor token, and gating it by accident removes the "any machine" property the recovery file exists to provide.

### What Phase 1 closes without naming it

Two older findings fall out of this work and should be ticked off rather than left open: **2**, because the render helper removes the fifteen independently invented tokens, and **3**, because the `1f916_reg` cookie gives each registration its own key and its own expiry.

Of the concurrency cluster, **19** and **20** are implied by the sweeper and the lock-ordering rule but are not named; **8** — `ReadToken` still an unzeroable `string` — has no home in this plan at all. Name all three in the commit, or say plainly that 8 waits for a later pass. An unnamed fix is one nobody can verify.

## Phase 1 review — commit `e694829`, 11 Aug

Read at `e694829c8cbb0395c1ab561b8b7daa2c3252bc74`. Three files: `cmd/client/main.go` (+4 −1), `internal/session/session.go` (+78 −8), `internal/web/handlers.go` (+325 −141). **No template was changed and no file was added.**

Seven of the nine claims hold up, and one unclaimed finding was fixed as well. Three problems came in with them, and two of the three block Phase 2.

### Blocking 1 — nothing compiled or ran this

The working log contains `git diff`, a series of reads, one edit, and `git commit -am … && git push`. No `go build`. No `go test`. That matters more than usual here, because this commit **changed the shape of a struct that other files use**:

- `Session.ReadToken` went from a `string` field to a `ReadToken()` method. Every `sess.ReadToken` read elsewhere in the tree must have gained parentheses.
- `Session.CSRFToken` was **deleted outright**. Every remaining reference is a compile error.
- `web.NewServer` gained two parameters.

`internal/session/session_test.go` was opened during the work and **not modified**, so if it touches either field the package does not build. `git commit -am` also stages only tracked files, so any new file this phase needed — a sweeper test, for instance — was silently left behind.

**Before anything else: `go build ./... && go test ./...`.** A push is not evidence that the code compiles.

### Blocking 2 — a nil session now dereferences instead of being rejected

`verifyCSRF` lost its session parameter. That parameter was doing a second job nobody wrote down: the old signature returned false when `sess == nil`, which meant every handler that called it was accidentally guarded against a missing session.

That guard is gone, and the handlers were not given a replacement:

```go
func (s *Server) handleRotatePost(w http.ResponseWriter, r *http.Request) {
    sess := s.getSession(r)
    if !s.verifyCSRF(w, r) {
        return
    }
    if sess.Repo == "" || sess.WriteToken() == "" {   // sess may be nil
```

The CSRF cookie has no expiry and the session is swept after thirty minutes, so this is not exotic: leave a tab open over lunch, come back, submit the form. `net/http` recovers the panic, so the result is a 500 and a stack trace rather than a dead process — but it is a crash on the most ordinary sequence a user can perform.

`handleRotatePost` is confirmed. **Every handler that calls `verifyCSRF` and then touches `sess` needs an explicit nil check**, and the sensible place is one at the top of each POST handler.

### Blocking 3 — `resolveRepo` puts the environment above the live session

```go
if formRepo != "" { return formRepo, true }
if s.envRepo  != "" { return s.envRepo, true }      // wins over the session
if sess != nil && sess.Repo != "" { return sess.Repo, true }
```

On a container started with `VAULT_REPO=A`, someone who types repository **B** on the login screen gets a session whose `Repo` is B — but `handleWriteTokenGet` and `handleWriteTokenPost` both resolve through this function and get **A**. So the write token is probed against A, `CheckIsNewStore` runs against A and seeds decoys there, while `handleRotatePost` reads `sess.Repo` directly and writes the vault to B.

The permission check and the write land on different repositories. **Correct order is form, then session, then pending registration, then environment** — the environment is a default, not an override.

### Worth checking, not blocking

- `(s *Server) sweeper()` exists and expires pending registrations at fifteen minutes, but the constructor fragment does not visibly start it. Confirm `go s.sweeper()` is called in `NewServer`, or the expiry is dead code. It also has no stop channel, unlike the session sweeper.
- Rotation re-reads from `main` rather than at the SHA returned by `PutBlob`. A stale read returns the **old** blob, whose secret will not match, so it fails towards the orphan-key screen — the safe direction, but a needless scare. One retry keyed on the returned SHA would remove it.
- The fresh recovery-file export that rotation is supposed to produce is still absent.
- `1f916_csrf` is a double-submit cookie with no binding to a session, so a local process can fetch a cookie and then use it — two requests instead of one. Local processes are already an accepted threat, so this is acceptable, but it should not be described as making the client safe against anything on the machine.

### Credit where it is due

- **Finding 23 was fixed without being claimed.** `handleRotateGet` now renders `s.getSessionView(sess)` instead of the whole struct — which also removes the hazard of handing a struct containing a live mutex to `html/template`.
- **A rejected write token no longer destroys the pending registration.** The probe-failure paths re-render the dialog and leave the record intact, so a mistyped token is now recoverable.
- **Rotation genuinely verifies.** `PutBlob` → `GetBlob` → decrypt → compare the secret → and only then assign to the session. The ordering detail that mattered most was honoured.
- **`SweepOnce()` exported is a better answer than the injectable clock I asked for.** A test can backdate `LastActive` and call it directly. Same result, less machinery.
- Lock ordering is manager-then-session in all five manager methods, `verifyCSRF` has no fallback, `1f916_reg` is its own cookie, and the orphan-key map is not swept.

### Phase 1.5 — commit `0e32aa4`, verified

All three blockers are resolved. Verified from the **commit diff**, not from the file: `raw.githubusercontent.com` served a stale copy of `handlers.go` after the push, still showing the pre-fix text, while `api.github.com/…/commits/0e32aa4` showed the real change. **After a fresh push, read the commit patch through the API and trust that; the raw endpoint lags and will make a landed fix look missing.**

- `resolveRepo` is now form → session → pending registration → environment. `resolveReadToken` was reordered the same way without being asked.
- The nil-session guard went into exactly five handlers: compose preview, compose publish, comment, vote, rotate. **It was correctly kept off `/login`, `/register`, `/recovery`, `/write-token` and `/acknowledge-key`**, all of which run before a session exists. A literal reading of "every POST handler" would have broken registration outright; that trap was avoided.
- `go s.sweeper()` is called at the end of `NewServer`, so the fifteen-minute pending-registration expiry is live.

Four things still to settle:

- **`handleLogout` was never audited.** It is registered for both `GET` and `POST` and has the same shape as the five that were fixed. Clicking log out on a tab left open past the idle sweep is exactly as likely as the rotate case. Check it, and every `GET` handler that dereferences the session.
- **The build was verified on the wrong Go version.** The Dockerfile pins `golang:1.23-alpine`; verification ran on `golang:1.25-alpine`. Code that compiles on 1.25 can fail on 1.23. Verify on the image the Dockerfile pins, or move the pin deliberately.
- **`-race` was never run and there is still no concurrency test.** Finding 20 is asserted, not demonstrated.
- `task.md` was written after `git commit -am`, which stages only tracked files, so it is almost certainly untracked and unpushed.

## What is right, and worth not breaking

- **CSP is stricter than specified** — `script-src 'none'` and `connect-src 'none'` rather than `'self'`. Correct, given there is no JavaScript. Keep it.
- **`GetMe` always sends `?since=`**, with the destructive `last_seen_at` side-effect documented at the call site.
- **Registration ordering is correct**: write token validated → decoys seeded → `/api/register` → vault `PUT`, with the raw-key fallback on *every* failure branch after the key exists. This was the hazard flagged during design, and it is handled.
- Send-token replay guard on publish; karma cached process-wide as one request; 10-second timeout and 8 MiB body cap on the board client; `Host` header check against DNS rebinding; the 404-versus-403 probe exists.

## Suggested order of work

*Revised after the midday re-audit — 1, 4, 5, 6, 7 and 9 are done.*

1. **16 first.** It blocks 15: registration is routed through `/write-token`, which has no session, so CSRF there has to work before that path is load-bearing.
2. **15, with 17 and 18 in the same pass.** All three are the same mistake — store configuration read from the environment inside a handler — and fixing 15 alone leaves decoys silently unseeded and rotation orphaning keys.
3. **21, then 19.** Both are claims the code does not yet honour.
4. **20**, with a concurrency test rather than a bare `-race` run.
5. Finish **2**, **3**, **8** — one token source, per-registration records, `ReadToken` as `[]byte`.
6. **10**–**14** and **22** as cleanup.

**On grouping these into one commit — 11 Aug.** A plan arrived proposing a first commit of "the concurrency and session fixes" plus 15, 17, 18 and 21, leaving 16 for later. Do not ship that combination. Finding 15 puts a **write-capable GitHub token field on the register screen**, and 16 is the finding that says the register and write-token POSTs have no CSRF check at all. Adding the field without the check makes an unprotected endpoint more worth reaching. 16 and 15 belong in the same commit, 16 first, exactly as step 1 above already says.