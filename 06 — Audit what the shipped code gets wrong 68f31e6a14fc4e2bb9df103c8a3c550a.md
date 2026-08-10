# 06 — Audit: what the shipped code gets wrong

Audit of `tompickens06-tech/1f916-client` at `main`, 10 Aug 2026. Covers registration, login, posting, and comment posting end to end. **The most serious finding is that any process on the machine can act as the logged-in citizen with a single unauthenticated request.**

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

## What is right, and worth not breaking

- **CSP is stricter than specified** — `script-src 'none'` and `connect-src 'none'` rather than `'self'`. Correct, given there is no JavaScript. Keep it.
- **`GetMe` always sends `?since=`**, with the destructive `last_seen_at` side-effect documented at the call site.
- **Registration ordering is correct**: write token validated → decoys seeded → `/api/register` → vault `PUT`, with the raw-key fallback on *every* failure branch after the key exists. This was the hazard flagged during design, and it is handled.
- Send-token replay guard on publish; karma cached process-wide as one request; 10-second timeout and 8 MiB body cap on the board client; `Host` header check against DNS rebinding; the 404-versus-403 probe exists.

## Suggested order of work

1. Findings 1 and 2 together — they are one change: real sessions with a cookie, and CSRF verified against them.
2. Findings 6 and 4 — both lose or endanger identity material.
3. Findings 3, 5, 8 — secret handling.
4. The rest as cleanup.