# Status

Where the work stopped and what comes next. Updated at the end of every stage.

## Stage 1 — skeleton and the first source. Done 2026-09-03.

The repository exists, `spoor ingest` reads Claude Code session logs into
SQLite, and `spoor count` says how many events are in there for a range of
days. Nothing else works yet — no clustering, no report, no interface.

### What is in place

- Go module, binary `spoor`, GPL-3.0-or-later, no cgo anywhere. CI
  cross-compiles for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64
  with `CGO_ENABLED=0` so that stays true.
- `internal/store` — SQLite schema, one `events` table plus import
  bookkeeping. Every column is plain text or an integer; the database is meant
  to be read with `sqlite3` and human eyes.
- `internal/source/claudecode` — the JSONL parser, written against the
  measured format rather than against any documentation.
- `internal/cli` — `ingest`, `count`, `version`.
- CI: gofmt, `go vet`, golangci-lint, `go test -race`, build, cross-compile.

### The events table

```
source        which plugin produced it; 'claude-code' is the only one so far
external_id   the source's own id. For Claude Code the JSONL uuid.
              UNIQUE (source, external_id) — this is what makes re-import free
ts            RFC3339, UTC, milliseconds, always Z, so string order is time order
duration_ms   NULL for a point in time, which is almost everything.
              Only system/turn_duration reports a real duration
type/subtype  the source's own classification, verbatim
project       guessed at ingest: the last path element of cwd. Refined later
raw_text      short metadata label — model, tool names, attachment kind.
              Never the text of a conversation
session_id, cwd, git_branch, entrypoint, is_sidechain, client_version
              Claude Code core fields, kept as columns rather than a blob
ingested_at   when spoor first saw it; the only trace left once the JSONL is gone
```

Nothing that does not exist in real data has a column. Fields the survey found
to be optional (`agentId`, `attributionSkill`, `slug`, `origin`, …) are not
read at all: they float by client, none of them is needed to reconstruct time,
and reading them would tie the parser to one client's habits.

### What counts as an event

A line becomes an event when it has both a `timestamp` and a `uuid`. That is
exactly the 12 field core, and it is a narrower rule than "every line with a
timestamp", on purpose:

- Lines with a timestamp but **no uuid** (`queue-operation`,
  `file-history-delta`) have no identity to deduplicate by, and a
  `queue-operation` carries the text of a queued prompt, which this project
  does not store. They are skipped.
- The **same uuid can appear in two files**: resuming a session replays part
  of the earlier transcript into the new session's log, same uuid and same
  timestamp, different `sessionId`. That is one event that was written twice,
  not two events, and counting it twice would inflate exactly the days on
  which sessions were resumed.

So an event count is always lower than a line count, by an amount that depends
on how often sessions were resumed. Both differences are deliberate.

### Acceptance

Verified against a real `~/.claude/projects` on 2026-09-03. The measurements
themselves live outside this repository, with the rest of the survey numbers.

| Check | Result |
|---|---|
| Two `ingest` runs in a row | identical event count; the second run inserted nothing, read no files, and finished in hundredths of a second |
| A week's count, both runs | identical |
| Same week recounted independently from raw JSONL | exact match |
| Event survives deletion of its JSONL | yes |
| Malformed lines | none |

The last two are also tests: `TestEventsSurviveSourceFileDeletion`,
`TestWeekSurvivesSourceFileDeletion`, `TestIngestTwiceGivesTheSameWeek`. All
of them run in CI.

### What is deliberately not done

- **No clustering, no durations, no report.** Events are stored as points;
  turning them into a day is the next stage but one.
- **`project` is the crude rule and nothing more** — the last element of
  `cwd`. Two directories with the same name collapse into one project. Real
  attribution comes later.
- **One source.** Browser, git and mtime follow, in that order, because that
  is the order in which they add hours.
- **No config file.** Everything is a flag or an XDG default. The first thing
  that genuinely needs configuring is the browser dictionary.
- **No plugin process boundary.** Sources are compiled in and live behind an
  ordinary Go package. Freezing the contract now, with one source to
  generalise from, would freeze the wrong thing.

### Loose ends for whoever comes next

- **`sessionId` and `session_id` do not always agree.** They coexist in CLI
  logs, and in a noticeable minority of lines they hold different values. The
  parser prefers `sessionId` (the core field) and falls back to the snake_case
  spelling. Nobody has checked which one is right when they differ. It matters
  as soon as anything groups events by session.
- **`project` is `basename(cwd)`.** Already ambiguous between a checkout and
  its worktree.
- **`turn_duration` carries a real `durationMs`.** Nothing uses it yet; it is
  the only honest duration in the data and the reporting stage should look at
  it before inventing durations from gaps.
- **Timestamps are stored in UTC, day boundaries are local.** `spoor count`
  converts. Anything else that thinks in days must do the same, or a late
  evening lands on the wrong date.
- **A symlinked `.jsonl` inside the source directory is read**, a symlinked
  directory is not walked. Neither can escalate anything — same user, same
  permissions, and no content is stored — but both are surprising.
- **`raw_text` keeps MCP tool names verbatim**, which look like
  `mcp__<server>__<tool>`. Those server names can be internal to whoever the
  user works for. Metadata by this project's rules, but worth knowing before
  showing the database to anyone.
- **A record whose field has an unexpected JSON type is dropped whole.**
  Missing fields are handled; a string where a bool belongs is not. Only
  `durationMs` is tolerant so far.
- **An unterminated line at the end of a file is re-read on every run.** That
  is correct — it is not a line yet — but if such a file stops growing, every
  run rescans up to the line-size cap for nothing. Wasted I/O, no data loss.
- **Schema changes need an entry in `addedColumns`.** `CREATE TABLE IF NOT
  EXISTS` does nothing to a table that already exists, so a new column never
  reaches an older database on its own, and the failure is quiet: ingest keeps
  exiting 0 and importing nothing. There is a test for the upgrade path; keep
  it honest.

## Next: the browser source

Second by importance and the only source that creates manual work: it accounts
for most of the time that no other source can name. It needs the port and the
first URL segment kept, and a domain→project dictionary in the config — which
is also where the config file finally appears.
