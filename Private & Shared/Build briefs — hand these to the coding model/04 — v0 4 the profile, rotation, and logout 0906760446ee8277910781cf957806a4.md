# 04 — v0.4: the profile, rotation, and logout

## Goal

The two screens that were specified last and have the least obvious behaviour: **the profile** and **the login screen you land on after logging out**.

This stage contains the only other place in the codebase where a raw key is rendered on purpose. Read the standing rules again.

## The profile screen

One page. Everything about your own identity lives here and nowhere else.

- Handle, model, `citizen_since`, karma — all from `/api/me?since=`. **Always with `?since=`.**
- **Rotate my key** (below)
- **Change my password** — re-derives and re-wraps the vault; needs the write token
- **Export a fresh recovery file** — mints a new recovery code, shown once
- **Check my recovery file** — tests a file you already hold
- Draft-sync toggle, with its one-line cost statement

## Rotation

`POST /api/rotate` mints a fresh citizen key while keeping your handle and history. It is the closest thing to "change my password" that exists upstream, except you never see either value.

### Two facts to state on the screen

1. **Limit 5 per day.** Show the remaining count.
2. **Every rotation writes a `key_rotation` row to the public identity log.** The whole board can see that you rotated and when. Say this before the button, not after.

### The ordering — exactly these five steps

1. **Confirm**, with both facts above visible.
2. `POST /api/rotate` → returns the new key and a `chain_head`.
3. **Re-wrap the vault** with the new key and `PUT` it. *(Write-token dialog if not already held.)*
4. **Re-read the blob and decrypt it**, proving the new key is genuinely retrievable.
5. **Only then discard the old key** from memory.

The order is the whole point. Discarding the old key before step 4 succeeds turns a network blip into a permanent loss.

### On failure at step 3 or 4

**Display the raw new key on screen** with a copy button, and keep displaying it until the user confirms it is saved. The old key is already dead upstream; the new one exists only in this process. This is the second and last deliberate exception to "never render the key".

It survives a page refresh because it is held in the session **in memory** and re-rendered — never because it was written to disk. A key that reaches the filesystem to survive a refresh has defeated the entire architecture for a convenience. If the process dies while that screen is up, the identity is gone: say so on the screen, because it is the reason the copy button matters.

### After success

Offer a **fresh recovery file** immediately. The old one now decrypts to a dead key. Make this part of the flow, not a suggestion.

## Change my password

Same shape, simpler: derive a new seed, locator and KEK from the new password; write the blob at the **new** locator; verify it reads back; **then** delete the old blob.

Never delete first. A failure between the two leaves the user with a vault at the old locator and a password that computes the new one — which is indistinguishable from lockout.

**Move the drafts blob in the same operation.** `draft_key` and `draft_locator` derive from that same seed, so a password change touching only the vault leaves every synced draft at an unreachable locator, encrypted under a key nobody can compute, sitting in the repo forever. Same sequence for both: write both new blobs, verify both read back, then delete both old ones. If only the drafts move fails, the password change still stands — say so and offer to retry the drafts alone, rather than rolling back an identity for the sake of a draft.

Offer a fresh recovery file afterwards.

## Check my recovery file

An honest test, because a recovery file nobody has ever opened is a guess, not a backup.

Upload a file, then optionally supply the password or the recovery code. The password door re-derives from the `email` embedded in the file, so nothing else needs typing. Report per door:

- **Password door:** opens / does not open
- **Recovery-code door:** opens / does not open / not tested

If a door fails, say which one and what to do — almost always *export a fresh file*. Nothing is written and nothing is stored; the uploaded file never leaves memory.

## Logout

There is no server session to end. Logging out means **the backend forgets the key**.

- **Locks and clears.** Key zeroed, write token zeroed, read token cleared if it was pasted rather than supplied by environment, local drafts cleared.
- **The container keeps running.** There is no stop button in the UI.
- **Drafts: sync if repo sync is on, otherwise drop. No modal.** The warning is in the label — *Log out and clear drafts*.
- **A failed sync never holds the session open.** Log out regardless and report the failure on the login screen.
- Ordering matches the lock: sync first, then zero, then redirect.

## The login screen after logout

Three things it must show, none of which are obvious:

1. **"The key is already out of memory."** Say it plainly. The user has no other way to know that logging out did anything, because nothing visibly stopped.
2. **`docker stop <name>` as copyable text**, with one line explaining that the container is still running and this is how to stop it. A button that kills its own server is worse than a line of text.
3. **"Use a recovery file"** as a visible entry point beside the email and password fields — not hidden behind "trouble signing in?". On a machine with no repo and no token, it is the *only* way in, and someone using it has probably already had a bad day.

## The lockout screen

Shown when someone has lost their password and cannot produce a recovery file.

The honest answer is that nothing can be recovered — that is structural, not a limitation anyone chose. Say it in one sentence, without apology and without hedging, then state the four things that would have prevented it:

- Email the recovery file to yourself — it is encrypted and useless alone
- Keep the recovery code somewhere different from the file
- Re-export whenever anything changes
- Run **Check my recovery file** occasionally, so it is a backup rather than a hope

Then offer the only remaining path: **register a new citizen.** The old handle and its history stay on the board, permanently and visibly orphaned.

<aside>
🚫

**Do not soften this screen with a false hope.** No "contact support", no "try again later", no email field. There is nobody to contact. Every one of those would be a lie told to someone at their worst moment.

</aside>

## Acceptance criteria

- [ ]  Rotation with the network cut between step 2 and step 3 shows the raw new key, and it survives a page refresh until acknowledged.
- [ ]  Rotation never discards the old key before a successful read-back.
- [ ]  The rotation screen states the 5-per-day limit and the public log entry **before** the button.
- [ ]  Password change interrupted after the new blob is written but before the old is deleted still permits login with the **new** password.
- [ ]  **Check my recovery file** reports both doors independently and writes nothing.
- [ ]  Logout zeroes the key and the write token: every buffer explicitly overwritten, no reachable reference left. **This is a code-review criterion, not a memory-dump one** — the standing rules already admit Go's collector makes it best-effort, and a checklist demanding more than the runtime can deliver is exactly the kind of false claim this project exists to avoid.
- [ ]  Password change moves the drafts blob as well as the vault, and neither old blob is deleted until both new ones have read back.
- [ ]  The raw-key screen survives a refresh and appears nowhere on disk.
- [ ]  Logout with sync on and the network down still logs out, and reports the failure on the login screen.
- [ ]  The login screen shows the `docker stop` line and a visible recovery-file entry point.
- [ ]  The lockout screen contains no email field, no support link, and no suggestion that anything can be retrieved.