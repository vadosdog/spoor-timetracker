# spoor

Reconstructs the working day after the fact, from digital traces already on
your disk. Nothing is started or stopped by hand, nothing runs in the
background, the screen is never captured.

**This is a personal tool, published in the open. It works for its author.
There are no guarantees, no support, and no promises about the next version.**

## State

Early. Right now `spoor` can do exactly two things: read Claude Code session
logs into a SQLite database, and tell you how many events that database holds
for a range of days. There is no report, no clustering and no interface yet.
See [docs/status.md](docs/status.md) for where the work stopped.

## Never

- **No network.** The tool does not go out, at all. Should that ever change,
  it will be off by default and said so here, above the install instructions.
- **Metadata only.** Times, paths, branches, domains, titles. The text of
  conversations, page contents and screenshots are out of scope — a boundary
  of the project, not a setting.
- **No cgo.** SQLite is `modernc.org/sqlite`, so a single static binary
  cross-compiles anywhere.

## Install

Requires Go 1.25 or newer.

```
go build -o spoor ./cmd/spoor
```

## Use

```
spoor ingest                                  # read sources into the database
spoor count --from 2026-08-25 --to 2026-08-31 # events in that range of days
```

`spoor ingest` is safe to run as often as you like: importing the same data
twice changes nothing. It is also worth running often — Claude Code deletes
its own session logs after 30 days, and only what has been imported survives.

## Where things live

Everything follows the XDG base directory spec:

| What | Where | Override |
|---|---|---|
| Database | `~/.local/share/spoor/spoor.db` | `SPOOR_DB`, `XDG_DATA_HOME` |
| Config | `~/.config/spoor/` | `XDG_CONFIG_HOME` |
| Claude Code logs (read) | `~/.claude/projects/` | `--claude-dir`, `CLAUDE_CONFIG_DIR` |

The database is meant to be read with your own eyes:

```
sqlite3 ~/.local/share/spoor/spoor.db 'SELECT ts, type, project, raw_text FROM events LIMIT 20'
```

No blobs, no serialised structs. If you cannot see what the tool collected
about you, that is a bug.

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).
