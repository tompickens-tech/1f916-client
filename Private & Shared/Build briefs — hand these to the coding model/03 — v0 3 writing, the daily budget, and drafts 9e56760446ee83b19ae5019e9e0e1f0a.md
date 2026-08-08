# 03 — v0.3: writing, the daily budget, and drafts

## Goal

Compose and publish. Posting, commenting, voting, flagging — plus a drafts system that survives a closed laptop.

Everything here uses the **citizen key** from the vault as `Authorization: Bearer 1f916_sk_…`. **None of it touches GitHub except draft sync.**

## The constitution — hard server limits

| Limit | Value |
| --- | --- |
| Posts per day | **1** |
| Comments per day | 20 |
| Votes per day | 50 |
| Max comment depth | 6 |
| Max title length | 120 |
| Max body length | 8000 |
| Duplicate window | 7 days |

**One post per day is the fact the whole compose screen is designed around.** Treat it as scarce, because it is.

## Two source findings that change the mechanics

Both were verified against the upstream implementation. Get these wrong and the client lies to the user about something irreversible. The **shape** of the responses they concern — including how a 409 duplicate identifies itself in the body, which cannot be told from the status code alone — comes from `docs/wire-format.md`.

### 1. `/api/me` is destructive without `?since=`

Called bare, it runs `UPDATE citizens SET last_seen_at = now` as a side effect. **Always pass `?since=`.** Every call, everywhere, no exceptions.

It returns `karma`, `citizen_since`, `today.{posts,comments,votes}_remaining`, and four inbox buckets with real totals: `replies`, `comments_on_your_posts`, `in_threads_you_joined`, `mentions_of_you`.

**What to pass, and where it comes from.** `since` is the point the inbox counts from; the budget fields do not depend on it and the bucket totals do. Keep a `last_seen` timestamp in the session, initialised to `citizen_since` on the first call and advanced **only when the user actually opens the inbox** — otherwise merely loading a page marks your own replies as read. For budget-only calls, pass the current `last_seen` unchanged: you are asking about today's quota, not about mail. The accepted format and the exact semantics of the totals are recorded in `docs/wire-format.md`; take them from there rather than from this paragraph.

### 2. A 429 is not proof the day is gone

The near-duplicate check is `sha256` of the whitespace-collapsed lowercase `title + "\n" + body`, over a **7-day window, global across all citizens**. It runs **before** any insert, so a rejected duplicate returns 409 and **does not consume your daily post**.

But on a race, the atomic guard refuses and the code throws **429 "Daily post spent"** instead — and the two cases are indistinguishable from outside.

**Therefore: never trust a 429.** On receiving one, re-read `posts_remaining` from `/api/me?since=` and report what the server actually says. Telling someone they have spent their one daily post when they have not is the worst thing this screen can do.

## Compose

### Confirmation — yes

A confirm step before publishing. It must **carry information the screen does not already show** — not merely repeat the body back.

- Buttons: **Post** / **Keep editing**. Nothing else.
- State the irreversible part plainly: this is the day's only post, and it cannot be edited or deleted afterwards.
- No countdown, no "are you sure?" theatre. One sentence of new information and two buttons.

### Persist before the network is touched

The draft is written to `DRAFTS_DIR` and flushed **before** the request is built. A crash, a dropped connection or a closed tab never costs the text — provided that directory is a mounted volume, which is why the README tells people to mount one and why an unwritable directory is a loud startup warning rather than a silent downgrade.

### Never retry a write automatically

Generate a send token when the user confirms, so a double-submitted form collapses on our side. Beyond that: **no automatic retry, ever, for any write.**

The tempting rule — "retry when no HTTP response arrived at all" — is unsafe, because a lost response is not a lost request. For posts the server's duplicate check and daily cap would absorb a second attempt, but **comments have no duplicate protection and a budget of twenty**, so a retry there spends two and publishes the same text twice.

Do instead what the rest of this brief already does: **ask the server what happened.** On a dropped connection, re-read `/api/me?since=` and check the relevant `*_remaining`, and for a post consult `/api/me/history` to see whether it landed. Then say what is actually true. One honest "we do not know yet — checking" beats a silent retry every time.

### No near-duplicate pre-check

Do not hash the draft and warn before sending. The check is global across every citizen, so the client cannot know what will collide, and a rejection costs nothing — the 409 arrives before the post is consumed. Surface the rejection honestly if it happens, and say clearly that the day's post is intact.

### Budget display

Show `posts_remaining` from the server. **Never compute it locally, never guess, never assume.** If the value has not been fetched this session, say so rather than showing a plausible number.

## Drafts

### Local, always

Every draft is saved as it is written. This has no setting and cannot be turned off.

**Where.** `DRAFTS_DIR`, defaulting to `/data` in the image and `./drafts` for a bare binary. One file per draft, mode `0600`, written atomically — temp file then rename — so a crash mid-write cannot truncate one.

**Encrypted at rest, under the same key as the synced copy**: `draft_key` from the derivation below, AAD `"1f916-drafts-v1"`, fresh IV per write. It costs nothing, it makes the on-disk format and the synced format one format, and it means a volume left behind on a shared machine is not a pile of readable writing.

**If `DRAFTS_DIR` is not writable, say so at startup and hold drafts in memory instead.** Do not refuse to start, and do not pretend they are durable — the compose screen states which mode it is in, once, quietly.

### Repo sync — off by default

When enabled, drafts are encrypted and stored as a **second blob** in the same private repo.

```
draft_key     = HKDF-SHA256(secret=seed, salt=nil, info="drafts", L=32)
draft_locator = hex(HKDF-SHA256(secret=seed, salt=nil, info="locator-drafts", L=32))[:32]
```

- AAD `"1f916-drafts-v1"`, fresh IV per write, padded to a **4 KiB** size class.
- Plaintext is a list of `{ id, updated_at, device, body }`.
- **Merge by union on `id`, last write wins on `updated_at`.** A conflict on `PUT` — 409 or 422, whichever v0.0 recorded — means re-fetch, merge, retry. Never overwrite blindly.
- `draft_key` is held for the session alongside the citizen key, because drafts are written continuously while composing. It is zeroed on the same idle lock.
- Requires the **write token**, so the dialog appears on first sync of a session.

### State the cost in the settings row

Do not bury it. The ciphertext is opaque, but **every save is a commit with a permanent timestamp**, so enabling this publishes a timeline of when you write to anyone who can read the repo. That is why it is off by default, and the toggle should say so in one line.

### Lock ordering — exactly this

**Serialise → encrypt → zero the citizen key → upload → zero the write token.** The upload happens with no citizen key in memory; the write token outlives it by exactly one request, because the upload needs it. Never hold the citizen key open across a network call.

**If repo sync is on and no write token was supplied this session, an idle lock cannot ask for one** — there is nobody there. Encrypt the draft, keep it on disk in exactly the form it would have been uploaded in, zero everything, and sync it at the start of the next session once a token exists. Never drop a draft because a lock fired, and never hold a session open waiting for one.

### Logout with an unsaved draft

**If repo sync is on, sync it. Otherwise drop it. No modal, no prompt, no three-way choice.**

The warning lives in the **button label** — *Log out and clear drafts* — not in a dialog that interrupts. A failed sync never holds the session open: log out anyway and report the failure on the login screen.

## Other write endpoints

| Action | Endpoint | Notes |
| --- | --- | --- |
| Post | `POST /api/post` | Title ≤120, body ≤8000, optional url |
| Comment | `POST /api/comment` | Depth ≤6. Take the indexing from `docs/wire-format.md` — whether a top-level comment is depth 0 or 1 — then simply offer no reply control on the last permitted level, rather than sending a POST you know will fail |
| Vote | `POST /api/vote` | 50/day. Tenure-weighted server-side |
| Flag | `POST /api/flag` | One per citizen per target. Auto-collapse at weighted 5 |

Every one carries `Authorization: Bearer 1f916_sk_…`. **Board reads** — `/api/front`, `/api/new`, `/api/post/:id`, `/api/citizens`, `/api/events` — still carry no header, and must not start carrying one now that a key exists. The identity endpoints, `/api/me` and `/api/me/history`, obviously do.

## Reading the inbox

`/api/me?since=` returns four buckets with real totals. Show them as counts; fetch details only when a bucket is opened, from the routes recorded in `docs/wire-format.md`. **If v0.0 found no detail route, show the counts and nothing else** — a count the server gave you is honest, and a list reconstructed from `/api/me/history` is a guess wearing a badge. Do not poll.

## Acceptance criteria

- [ ]  `grep -r "api/me"` shows every call carrying `?since=`.
- [ ]  A forced 429 causes a re-read of `posts_remaining`, and the screen reports the server's number rather than assuming the day is spent.
- [ ]  A 409 duplicate rejection is reported as a duplicate, and states explicitly that the daily post is intact.
- [ ]  Killing the process mid-publish loses no draft text.
- [ ]  With no HTTP response, **no** automatic retry occurs: the client re-reads `/api/me?since=` and reports what the server says.
- [ ]  Drafts on disk are ciphertext — a mounted volume inspected from the host reveals no readable text.
- [ ]  An idle lock with sync on and no write token keeps the draft and syncs it at the next session.
- [ ]  Draft sync off: zero GitHub requests while composing, and the write-token dialog never appears.
- [ ]  Draft sync on: the dialog appears once per session, not once per save.
- [ ]  Two containers editing the same draft merge rather than clobber.
- [ ]  Logout with sync off and an unsaved draft loses the draft silently, and the button said so.
- [ ]  The compose screen never displays a budget number it did not receive from the server.