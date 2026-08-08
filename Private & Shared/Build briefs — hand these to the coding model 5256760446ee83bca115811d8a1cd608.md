# Build briefs — hand these to the coding model

<aside>
⚠️

**Do not hand the tickets to a coding model.** They are a decision log — every reversal is still on the page beside what replaced it, so a model reading them finds dead configuration, dropped platforms, and refused proposals sitting next to their refusals. It will build some of what we decided against. These briefs are the same decisions with the deliberation stripped out and only the surviving answers kept.

</aside>

## How to use these

Six stages, in order. Each brief is self-contained: what to build, the decisions that constrain it, and how to tell when the stage is done. A model should never need to open a ticket to finish a brief.

**Include the standing rules in every prompt, at every stage.** They are the rules a model breaks by being helpful, so they have to be repeated rather than referenced once.

## The order, and why it is this order

1. **v0.0 — pre-flight.** Verify both APIs and write down what they actually return. No application code.
2. **v0.1 — the board, read only.** No secrets, no repos, no token, no crypto.
3. **v0.2 — identity.** Vault, registration, login, recovery file.
4. **v0.3 — writing.** Compose, the daily budget, drafts.
5. **v0.4 — the profile.** Rotation, logout, recovery.
6. **v1.0 — publish.** Docker, Actions, GHCR, README.

v0.0 comes first because the later briefs describe endpoints without giving their field names, and the vault design rests on five unverified facts about GitHub — one of which may make its own safety check a false green. Guessing in the read path is a bug; guessing in the vault path is a citizen nobody can log in as. It costs an hour and produces `docs/wire-format.md`, which outranks every brief on any question of what a server returns.

v0.1 comes next because reading 1f916 requires no key at all. Every architectural bet — the stack, the templates, the escaping, the design language, the container — gets tested against the real board while the codebase still contains nothing that can hurt anyone. Building identity first means writing the dangerous part before knowing whether the boring parts work.

## If a brief and a ticket disagree

**The brief wins.** Tickets record how a decision was reached, including the ones that were reversed. Briefs record what to build.

And if a brief disagrees with `docs/wire-format.md`, **the captured response wins.** A brief describes intent; that file describes what the server actually returned. Record the contradiction there rather than building silently around it.

[00 — Standing rules (include in every prompt)](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/00%20%E2%80%94%20Standing%20rules%20(include%20in%20every%20prompt)%209d56760446ee8305b2d081d2d763c3f2.md)

[02 — v0.2: identity, the vault, and the two tokens](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/02%20%E2%80%94%20v0%202%20identity,%20the%20vault,%20and%20the%20two%20tokens%20eeb6760446ee8218b71b8177d64f89e9.md)

[01 — v0.1: the board, read only](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/01%20%E2%80%94%20v0%201%20the%20board,%20read%20only%20bb96760446ee830c864781da5f56846c.md)

[04 — v0.4: the profile, rotation, and logout](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/04%20%E2%80%94%20v0%204%20the%20profile,%20rotation,%20and%20logout%200906760446ee8277910781cf957806a4.md)

[03 — v0.3: writing, the daily budget, and drafts](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/03%20%E2%80%94%20v0%203%20writing,%20the%20daily%20budget,%20and%20drafts%209e56760446ee83b19ae5019e9e0e1f0a.md)

[05 — v1.0: Docker, Actions, and publishing](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/05%20%E2%80%94%20v1%200%20Docker,%20Actions,%20and%20publishing%209a16760446ee828aad9281d59e3a7be9.md)

[00b — v0.0: pre-flight, before any code is written](Build%20briefs%20%E2%80%94%20hand%20these%20to%20the%20coding%20model/00b%20%E2%80%94%20v0%200%20pre-flight,%20before%20any%20code%20is%20written%20f3ccb94fd3764007aa4721d6d6eb6ad1.md)