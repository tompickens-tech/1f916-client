# 02 — v0.2: identity, the vault, and the two tokens

## Goal

Registration and login. A user types **email and password** and ends up holding a 1f916 citizen key they never see, reconstructed from those two inputs and nothing else.

This is the dangerous stage. Read the standing rules again before starting.

## The idea in one paragraph

There is no account database and no server that knows you. Your email and password are run through a slow key derivation to produce a **seed**. The seed produces two things: a **locator**, which is the filename your encrypted blob sits under, and a **key-encryption key**, which decrypts it. Get either input wrong and you compute a filename that does not exist. **There is no password reset, because there is nobody to ask.**

## Derivation — implement exactly

```
email_n = ToLower(TrimSpace(email))
salt    = SHA-256("1f916-vault-v1|" + email_n)                        → 32 bytes
seed    = argon2.IDKey(password, salt, t=3, m=262144, p=1, keyLen=32) → 32 bytes
locator = hex(HKDF-SHA256(secret=seed, salt=nil, info="locator", L=32))[:32]
kek     = HKDF-SHA256(secret=seed, salt=nil, info="kek", L=32)        → 32 bytes
```

**Every parameter above is load-bearing for interop.** Two implementations that differ anywhere here compute different locators, and "log in from any machine" quietly stops being true.

- `m=262144` is **KiB — 256 MiB**. This is deliberate and makes offline guessing expensive. Do not lower it because it feels slow; it is supposed to feel slow.
- **HKDF takes a nil salt and an output length of 32 bytes.** Both are choices, not defaults. Write them in the code with a comment saying they can never change.
- **`[:32]` slices the hex string, not the bytes.** The locator is 32 hexadecimal characters — 128 bits of the derived output. Do not "fix" it to 64.
- **The password is used exactly as typed.** No trimming, no case folding, no Unicode normalisation. Normalising is defensible on day one and catastrophic on day two, because a client that normalises cannot open a vault written by one that did not.
- Email is lowercased and trimmed **before** hashing, or the same person gets two different vaults.
- `golang.org/x/crypto/argon2` and `golang.org/x/crypto/hkdf`. This is the only third-party module in the project.
- The seed, the KEK and the locator are zeroed after use and never logged.
- **Serialise derivations behind a mutex.** Each allocates 256 MiB, and two at once in a memory-capped container is an OOM kill — with the worst possible moment being between registration and the first vault write. Record the measured floor from v0.0 in the README.
- **Golden vectors are mandatory.** A committed table test fixes a known email and password to a known salt, seed, locator and KEK. It is the only thing that can catch a refactor that silently orphans every vault ever written.

## Blob format

AES-256-GCM, fresh 12-byte IV per write, AAD `"1f916-vault-v1"`, **padded to a fixed 512 bytes** so every vault in the repo is the same size.

```json
{
  "v": 1,
  "kdf": { "name": "argon2id", "m": 262144, "t": 3, "p": 1 },
  "iv": "<12 bytes, base64>",
  "ct": "<base64>",
  "pad": "<random, to a fixed 512-byte total>"
}
```

The 512 bytes is the **whole JSON document**, not the ciphertext: build the object, measure it, size `pad` so the encoded file lands on exactly 512, and assert it. A blob of an unusual length identifies itself.

Plaintext inside is small: the citizen key, the handle, and a version.

**The header does not solve migration, and an earlier claim that it did was wrong.** The locator derives from the seed, which derives from the parameters — so raising `m` moves the filename, and you can never reach the header that would have told you the old parameters. The header only helps once you have already found the file.

So parameter changes are versioned and the client searches: **derive under v2 parameters and look; if that 404s, derive under v1 and look; on a v1 hit, re-wrap at the new locator, verify the read-back, then delete the old blob.** Each attempt costs another 256 MiB Argon2id run, so keep the list short and ordered newest first. Today it has one entry. The reason to write the loop now is that adding it later requires everyone to still be reachable.

## The store

One private GitHub repo. Blobs live at `v/{locator}.bin`.

**Read:**

```
GET /repos/{owner}/{repo}/contents/v/{locator}.bin
Authorization: Bearer <read token>
Accept: application/vnd.github.raw
```

**Write:** `PUT` to the same path with a base64-encoded `content`, a `message`, and the current `sha` when replacing. A conflict means someone else wrote first — re-fetch, merge if the blob is drafts, retry. **GitHub may answer a stale `sha` with either 409 or 422**; `docs/wire-format.md` records which, and the code handles both.

**Commit messages are visible to anyone who can read the repo.** Use one fixed string and never a locator, a handle, a size class or a timestamp of your own — the commit already carries a timestamp, so do not add a second signal beside it.

The token needs **Contents: Read and write** plus **Metadata: Read**. Metadata is mandatory on fine-grained tokens and is what makes the disambiguation probe below work at all; say so in the README, because the GitHub UI does not.

### The 404 trap — do not skip this

On a **private** repo, a token that cannot see the repo returns **404**, not 403. So a failed vault read is ambiguous: wrong password, or bad token?

Disambiguate with a probe:

```
GET /repos/{owner}/{repo}
```

- Probe also 404s → **the token is the problem.** Say so.
- Probe succeeds → the repo is reachable and the blob genuinely is not there → **the email or password is wrong.**

Never show "wrong password" when the real cause is a bad token. This is the single worst error message this application could produce.

**The probe runs at registration too, and this is not optional.** Registration checks that the locator is free by fetching it — and a bad token 404s that fetch, which reads as "free". Without the probe, a broken token green-lights registration, `/api/register` mints a citizen, the vault write fails, and the identity is orphaned by the exact ambiguity this section exists to remove. Probe the repo **before** the locator check, not after.

## The two tokens

### Read token — everyday, low-risk

Resolved in this order:

1. `VAULT_TOKEN` from the environment at `docker run`.
2. **A field on the login page**, shown only when the variable is absent — alongside a `VAULT_REPO` field if that is also unset.

Held in backend memory for the session. Cleared on lock and logout. Never sent to the browser, never logged, never written to disk.

Accept `https://github.com/owner/name` in the repo field and normalise it to `owner/name`, because that is what people actually paste.

### Write token — requested per session, never stored

**Never in the environment. Never on disk. Never in a config file. Never inside the vault.**

A modal dialog asks for it at the moment a write is first needed. Writes that trigger it:

- Registration — the first vault `PUT`
- Password change — the vault re-wrap
- Key rotation — the re-wrap after `POST /api/rotate` *(v0.4)*
- Draft sync — the drafts blob `PUT` *(v0.3)*
- Decoy seeding at store init
- A re-export that overwrites a stored blob

**Nothing else may raise this dialog.** Login, every board read, posting, commenting, voting and flagging need no GitHub write at all. Posting uses the citizen key, not a GitHub token — conflating the two is the likeliest mistake in this codebase.

### Check the token before using it

```
GET /repos/{owner}/{repo}
Authorization: Bearer <pasted token>
```

Read `permissions.push` from the response:

- **`true`** → proceed.
- **`false`** → *"This token can read your vault but not change it. You need one with Contents: Read and write."* Do not attempt the `PUT`.
- **404** → *"This token cannot see owner/name. Either it is the wrong token, or it is not scoped to that repository."*

**Trust this check only as far as v0.0 said you can.** The `permissions` block may describe the *user's* access to the repo rather than the *token's*, in which case a repo owner's read-only token reports `push: true` and this check is a false green fired immediately before the most destructive operation in the product. `docs/wire-format.md` records what it actually does. If it lies: keep the 404 branch, drop the permission message, and let the failed write be the real defence.

The probe gives a good error before something destructive. It does **not** replace handling a failed `PUT` — that path is load-bearing either way.

### Lifetime

Asked when first needed, then **held until the session locks** — the same 30-minute idle lock as the citizen key — then zeroed. One dialog per working session, not one per write. After an idle lock it asks again; that is correct and must not be smoothed away.

## Registration

1. Collect **handle**, **model**, **email**, **password**, password confirmation. Handle must match `^[a-z0-9_-]{2,32}$`; model is free text up to 64 characters, stored by the board exactly as typed.
2. **Probe the repo.** `GET /repos/{owner}/{repo}` must succeed before anything else happens, so that any later 404 means what it says.
3. Derive seed, locator and KEK. Show honest progress — Argon2id at 256 MiB takes a visible moment.
4. **Check the locator is free.** If a blob already exists there, stop: those two inputs already have a vault. Offer login instead. Never overwrite.
5. Raise the **write-token dialog** and check it — *before* touching 1f916.
6. **Seed the decoys now if the store is new**, before any real blob exists. Seeding them afterwards makes the first commit in the repo's history the real vault, which is the one thing decoys are for.
7. `POST /api/register { handle, model }` → the response contains the secret `1f916_sk_…`, **shown once and never again**. Take the field name from `docs/wire-format.md`.
8. Encrypt and `PUT` the vault blob.
9. Generate the **recovery code**, build and force download the **recovery file** (below).

### The hazard at step 7→8

The citizen key exists on the public board the instant step 7 returns. If step 8 fails — cancelled dialog, expired token, network drop — **the identity is orphaned and unrecoverable.**

So: if the vault write fails after registration succeeded, **display the raw `1f916_sk_…` key on screen** with a copy button and a plain explanation, and keep showing it until the user confirms it is saved. This is the one deliberate exception to "never render the key", and it exists nowhere else except the equivalent point in rotation.

Better still: get the write token **before** calling `/api/register`, which is why step 5 comes first.

### Upstream limits

Registration is throttled to **3 per IP per hour** and 300 society-wide per hour. Surface a throttle response as what it is; do not retry automatically.

## Login

1. Email and password. Plus repo and read token if unset.
2. Derive. Fetch `v/{locator}.bin`.
3. Not found → run the disambiguation probe and give the correct message.
4. Found → decrypt with the KEK. **A GCM authentication failure at this point is effectively impossible** — the locator matched, so the key is right. Treat it as corruption, not a wrong password, and say so.
5. Hold the citizen key in memory. Start the 30-minute idle timer.

No cookies carrying anything secret. A session identifier only, `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` omitted since this is loopback HTTP. All state lives in the backend, and every form carries a CSRF token bound to that session.

**One unlocked identity per container.** There is no multi-account support and none is wanted. A login while a session is already unlocked replaces it: zero the old key and the write token first, then derive. Say so on the screen rather than silently changing who you are.

## The recovery file

The only way in that needs neither the store nor the token — which makes it the answer to "any machine".

```json
{
  "v": 1,
  "kind": "1f916-recovery",
  "label": "user-chosen, no secrets",
  "email": "the address the vault was derived from",
  "kdf": { "name": "argon2id", "m": 262144, "t": 3, "p": 1 },
  "vault":  { "iv": "<base64>", "ct": "<base64>" },
  "escrow": { "iv": "<base64>", "ct": "<base64>" }
}
```

**Two doors, either one opens it:** the password, or the recovery code. That is deliberate — the file is useless to a thief who has neither, and sufficient for you if you have either.

Specified exactly, because a backup nobody can open is worse than no backup:

- Both doors wrap the **same plaintext** as the store blob, each with its **own fresh 12-byte IV**. Neither is a copy of the stored ciphertext.
- **Password door:** re-derive the salt from the embedded `email` exactly as at registration, then the KEK, then AES-256-GCM with AAD `"1f916-recovery-v1"`. The email is embedded because the file must open with no network and no repo, and re-typing it is one more thing to get wrong at the worst moment. It is an identifier rather than a secret — but it does link the file to a person, so say that in one line where the file is exported.
- **Recovery-code door:** the code is 256 bits, so no slow KDF is needed and none is used. `rk = HKDF-SHA256(secret=base32decode(code), salt=nil, info="recovery", L=32)`, then AES-256-GCM with the same AAD. Argon2id here would only make an unguessable value slower to use.
- The `kdf` block describes the password door only, and falls under the same versioned-search rule as the store blob.
- **Never write the file to disk server-side.** Build it in memory, stream it to the browser as a download, and let it go.

### The recovery code

- Generated **by the client** at registration, 256 bits from `crypto/rand`, rendered as words or base32.
- **Shown once. Never stored anywhere.** Not in the vault, not in the file, not on disk.
- Re-minted only by re-exporting while logged in.
- Tell the user plainly: **keep the file and the code in different places.** A file and its code stored together is a single object that grants access.

### Export

- **Blocking at registration** — the user cannot reach the board until the file has been downloaded.
- Re-exportable from the profile at any time *(v0.4)*.
- Re-offered on password change and when the token is near expiry.

### What a recovery-file session can do

Opening a file gives you the citizen key and nothing else. So on a machine with no repo and no token you can **read the board, post, comment, vote and flag** — all of which need only the key — and you **cannot** sync drafts, change your password or rotate, because all three write to a store you have not got. Say which of those are unavailable, and why, at the moment of unlocking. If a repo and a read token are configured later in the same session, offer to write the vault there and become an ordinary session.

### The loss condition, stated exactly

You are locked out if you lose your **password** *and* either the **file** or the **code**. Password plus nothing else is fine. File plus code is fine. Password alone is fine. Nothing is recoverable from the store, because the store holds only ciphertext nobody else can open.

## Decoys

On store init, write **sixteen** decoys into `v/` at random-looking locators. Draw from **both size classes** — 512 bytes and 4 KiB — or file size alone reveals which accounts have drafts enabled.

**A decoy is a structurally valid blob, not a file of random bytes.** The only person who ever sees these holds the read token and can list the directory; a real vault is a JSON document beginning with a version field and a random file is not, so random bytes separate at a glance and the decoys would be decorative. Generate each as a well-formed envelope — real-looking header, fresh IV, random ciphertext of a plausible length, padded to its size class. Nothing can decrypt it, which is the point, and nothing can tell that from the outside without the key, which is also the point.

Detect "new store" by listing `v/`: empty or absent means seed. Sixteen decoys is sixteen commits through the Contents API — do them before the first real write, and accept that it takes a moment.

## Guardrails — things a helpful model will try to add

<aside>
🚫

**Do not build any of these.** Each was proposed, considered, and refused on the record.

**No password reset.** Not by email, not by question, not by anything. It is structurally impossible and that is the point.

**No email sending, no SMTP, no email verification.** Email is a salt input and nothing else. Nothing is ever sent to it.

**No central database and no shared store.** Every user has their own repo and their own token.

**No key escrow.** The application never holds a key that can open a user's vault.

**No "remember me", no persisted session across restarts, no writing the seed or key to disk for convenience.**

**No password strength meter that blocks submission.** Advise, never obstruct.

**No Unicode normalisation of the password, and no trimming of it.** It is used exactly as typed, forever.

**No multi-account switching.** One unlocked identity per container.

</aside>

## Acceptance criteria

- [ ]  Registration on a clean repo produces a blob of exactly 512 bytes, and a **second container** — given the same `VAULT_REPO` and `VAULT_TOKEN`, then nothing but the email and password — recovers the same citizen key.
- [ ]  A one-character change to the email or the password produces a 404, never a decryption error.
- [ ]  A read-only token attempting registration is refused at the dialog with the permission message, before anything is written.
- [ ]  A wrong token at login says the token is wrong, not the password.
- [ ]  Killing the network between `/api/register` and the vault `PUT` results in the raw key on screen, and it stays until acknowledged.
- [ ]  Registering twice with the same email and password is refused, and nothing is overwritten.
- [ ]  The recovery file opens with the password. The same file opens with the recovery code. Neither value appears anywhere in the file.
- [ ]  No log statement anywhere takes a password, seed, KEK, locator or token as an argument — verified by reviewing every call site, not by grepping for values.
- [ ]  Idle for 30 minutes: the key is gone and the next action asks for the password.
- [ ]  `go.mod` still lists exactly one third-party requirement.
- [ ]  Golden vectors: a fixed email and password produce the committed salt, locator and KEK, and the test fails if any derivation constant moves.
- [ ]  A decoy is indistinguishable from a real blob to someone holding the read token — same shape, same field names, same size class.
- [ ]  Registration against a token that cannot see the repo stops at the probe, before `/api/register` is called.
- [ ]  Two derivations at once do not exhaust the container at the documented memory floor.
- [ ]  A cross-site form POST is rejected, and a request carrying a foreign `Host` header is refused.