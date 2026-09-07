# Working on spoor

Read this before touching anything in this repository. These are invariants,
not preferences: breaking one is a reason to stop and ask, not to do it and
explain afterwards.

## What this is

`spoor` reconstructs the working day after the fact, from digital traces
already on disk: Claude Code session logs, git, browser history, the calendar.
Nothing is started or stopped by hand, nothing runs in the background, the
screen is never captured.

The machine recovers **when and how long**. The person adds **what it was** —
once, at the end of the day, and only where the rules fell short.

A personal tool, published in the open. In that order: it works for its author
first, other people like it second.

## Hard limits

1. **Not one personal path, token or name in this repository.** Anything
   machine dependent lives in the config. Tests run on synthetic fixtures,
   never on a dump of real sessions.
2. **No network without explicit permission.** By default the tool does not go
   out at all. When an LLM mode appears it will be off, and README will say so
   above the install instructions.
3. **Idempotence.** A second run over the same data gives the same result and
   creates no duplicates. Proven by a test, not by eye.
4. **Reproducible linking.** Two runs over the same data, one result. Event
   linking is therefore deterministic — timestamps, regexes, a dictionary in
   the config. No LLM in that path.
5. **The database is read with human eyes.** A user must be able to open the
   SQLite file and understand everything collected about them. No blobs, no
   serialised structs.
6. **Everything the TUI does is available as flags.**
7. **A source declares the systems it supports** and stays quietly out of the
   way elsewhere, rather than failing.
8. **Metadata only.** Domain, title, path, branch, time. The contents of
   conversations, screenshots, page text — no. This is a boundary of the
   project, not an optimisation.
9. **No cgo. Ever.** SQLite is `modernc.org/sqlite`. cgo breaks
   cross-compilation and forces a build runner per platform.

## Review before every push

Every change is reviewed by a **separate review agent before it is committed
and pushed**. Two kinds of review, and a change that touches both code and
documentation gets both.

**Code review**, with security and anonymity as the first priority, ahead of
style and architecture. The reviewer looks for:

- personal paths, host names, user names, emails, tokens, real session ids or
  anything else that identifies the author or their employer — in code,
  comments, fixtures, test data and commit messages alike;
- conversation content, page text or file contents leaking into anything the
  tool stores or prints;
- any code path that could reach the network;
- SQL built by string concatenation, unchecked file paths, world-readable
  files holding personal data.

**Documentation review**, for anything a reader is expected to follow. Prose
gets a separate pass because prose fails differently from code, and nothing
compiles it:

- every claim checked against the source, not against intent. Quoted output
  must be what the program actually prints;
- every command run as written, on a real terminal. A snippet that works in a
  script can still fail when pasted into an interactive shell;
- promises kept. A section that says "check this for yourself" must actually
  show the reader enough to check;
- structure and order: prerequisites before install, no forward references, no
  saying the same thing four times;
- English, since it is nobody's first language here.

Both of these have already earned their place. The code review caught a
missing schema migration that would have made `ingest` exit 0 while importing
nothing. The documentation review caught three statements that did not match
the program, a transparency promise that showed a third of the table, and an
inspection command that bash refused to run at all.

No push without the relevant pass. If the reviewer finds something, it is
fixed and reviewed again — the review is a gate, not a formality.

## Layout

```
cmd/spoor            entry point, nothing but wiring
internal/cli         commands and flags
internal/event       the record every source produces
internal/store       SQLite: schema, inserts, queries
internal/source/…    one package per source; claudecode is the first
internal/paths       XDG locations
docs/status.md       where the work stopped and what is next
```

## How to work

- **One stage at a time.** The roadmap is cut so that each stage leaves a
  working tool. "While I am here I will also do the next one" destroys that.
- **Crude rules first.** Look at where they lie on real data and fix what
  actually broke. Clever heuristics on day one are a listed risk.
- **Derive from measurements, not from the concept.** The JSONL format was
  measured before the parser was written; the numbers, not the docs, are what
  the code follows.
- **Ask instead of guessing.** Accounts, paths to work repositories,
  thresholds — ask the author.
- **Update `docs/status.md` at the end of a stage.** In a month the context
  will be gone.
- **English in this repository** — code, comments, docs, commit messages.
  The audience is international.

## What the Claude Code source knows about its own format

Measured, not assumed. Ignoring any of these produces a bug:

- Read **only the 12 field core**: `type uuid parentUuid timestamp sessionId
  version cwd gitBranch entrypoint isSidechain userType` plus one of
  `message` / `attachment` / `subtype`. It is identical across all four
  clients and has never changed. Everything else is optional and its absence
  is not an error.
- The field set is decided by the **client** (`entrypoint`), not the version.
  Versions are not monotonic in time; comparing "adjacent versions" is noise.
- The CLI writes both `sessionId` and `session_id`. Accept both.
- `system` is **two incompatible shapes** under one type — hooks and
  compaction. Tell them apart by `subtype`.
- Records **without `timestamp`** (`last-prompt`, `ai-title`, `mode`, …) are
  session state, not events. There are thousands. Skip them silently.
- The same `uuid` can appear in two files when a session is resumed. That is
  one event, not two, which is why `(source, external_id)` is unique.
- Claude Code **deletes its own logs after 30 days**. The database accumulates
  rather than being rebuilt on demand; an imported event must outlive the file
  it came from. There is a test for exactly that.
