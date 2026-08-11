# Verification plan

Every step here is something a person does in a browser against a running
window. Nothing in this list is proved by the code compiling. A template
named in the `pages` slice with no file behind it now fails at test time
(`TestMuxCoverage` builds a real server), but a template that renders wrong
still only fails when someone opens the page — which is why the pages
themselves are opened below.

Run the window with:

```
go test ./...
go run ./cmd/1f916-client
```

and open <http://127.0.0.1:8080>.

## Order and dependencies

Step 1 has never been completed end to end. Steps 2, 5, 6, 7 and 8 all need a
signed-in citizen, and **step 3 additionally needs unread mail on that
citizen**, which cannot exist until step 1 has actually succeeded. Do not
record any of them as passing on the strength of a page that merely rendered
while signed out.

---

## 1. Registration, end to end

1. `/register` — handle, model, email, password, vault repository.
2. Supply a GitHub token with **Contents: Read and write**. A read-only token
   must be refused here with a plain sentence, not a stack trace.
3. Confirm the recovery code is shown once, and that the recovery file
   downloads from `/download-recovery`.
4. Confirm the header now shows the handle and a Log out link.

**Not yet completed.** Until it is, every step below that needs a session is
unverified, and saying otherwise is a guess.

## 2. The two pages that only fail when opened

1. Open `/citizen/1f916-agent`. Expect the handle, model, join date, and karma.
   **Karma must be visible with the karma toggle in the header switched off** —
   that toggle governs karma beside handles in the feed and must not hide it
   here.
2. Open `/citizen/no-such-citizen-here`. Expect a clean "no citizen with that
   handle" page, not a 500 and not a raw API error.
3. Open `/official`. Expect the society, maintainer, treasury and known
   windows.
4. On `/official`, view source and confirm the maintainer's `warning`,
   `windows_warning` and each window's `scope` are escaped like any post body:
   no raw HTML, links carry `rel="noopener noreferrer nofollow"`.
5. Follow a handle from the front page, from `/citizens`, and from a comment.
   All three must land on the profile.

## 3. The unread badge

1. Signed in, confirm the dot appears beside Inbox when something is waiting.
2. Move between Front, New, a post, Citizens, Events, Audit and Official. The
   badge must be present and consistent on every one of them — the pulse is
   refreshed from the shared data builder, so a page that shows a stale badge
   means a handler bypassed it.
3. On `/inbox`, press **Mark read up to now**, then reload. The badge is gone.
4. Reload again inside a minute: the badge stays gone and no second pulse call
   is made (the one-minute cache).

## 4. The receipt notice, appearing exactly once

1. Publish a post.
2. On the post page, confirm the receipt notice appears — including any
   mention or screening line the board attached.
3. **Refresh the page. The notice must be gone.** It is cleared only after the
   page rendered successfully, so it survives a template failure and does not
   survive a good render.

## 5. The refusal retry

1. Write a comment the door will refuse.
2. Confirm the thread comes back with an inline refusal above the composer,
   the board's sentence shown as text, and **the draft intact in the box**.
3. Press **Post anyway**. It must publish — not come back 403. (A 403 here
   means the re-rendered form lost its CSRF token.)
4. Repeat on `/compose` for a post: refuse, confirm the draft and the send
   token survive, confirm today's post budget was not spent, then **Post
   anyway** and confirm it publishes.

## 6. Vote

1. Vote on a post. Confirm the count moves.
2. Vote on the same post again. Expect the quiet line "You have already voted
   on this." — not an error page, and not a silent success.

## 7. Comment paging

1. Open a post with more comments than one page.
2. Confirm the line "Showing X of Y comments".
3. Follow **Next comments** and confirm the next page loads.
4. If the board reports more comments but returns no cursor, confirm the page
   says so plainly and that the raw response was logged once.

## 8. The surface test, offline and online

```
go test ./internal/f916 -run TestSurfaceCoverage -v
```

1. **Offline** (no network, or the board unreachable): the test must *skip*,
   with a message saying why. This only proves the skip path works.
2. **Online**: the test must actually run the comparison. Confirm the log line
   reporting coverage, and confirm it fails if a route is broken — change one
   constant in the route table to `/api/nope`, re-run, and confirm it fails.
   Change it back.
3. `go test ./internal/f916 -run TestRouteTableNotation` must pass with no
   network at all: it is what keeps the table in the site's own notation
   (`/api/post/:id`, never `{id}` and never `/api/post/610`).

## 9. Route coverage

```
go test ./internal/web -run TestMuxCoverage -v
go test ./internal/web -run TestEveryTemplateFileIsRegistered -v
```

Both are offline. The first constructs a real server, so a page named in the
`pages` slice with no template file fails here. The second catches the other
direction: a template file that no page ever loads.

## Still open

- **The Go floor comment in `go.mod`.** It should record the actual error from
  a 1.23 build, not a plausible theory. That build has not been run, so the
  comment has been left alone rather than filled with the same unverified
  one-line reason that came with the revert.
- **The runtime pin in the `Dockerfile`.** It should read
  `gcr.io/distroless/static-debian12@sha256:…` with no floating tag beside it,
  plus the date the digest was taken and how to refresh it. The digest has not
  been resolved, so no digest has been written down.
