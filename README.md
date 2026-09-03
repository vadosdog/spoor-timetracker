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

Requires Go 1.25 or newer. There are no releases yet; build it yourself.

```
git clone https://github.com/vadosdog/spoor-timetracker
cd spoor-timetracker
go build -o ~/.local/bin/spoor ./cmd/spoor
```

Put the binary wherever your `PATH` points; `~/.local/bin` is only a common
choice. The rest of this file assumes `spoor` is runnable by name.

## Use

```
spoor ingest                                  # read sources into the database
spoor count --from 2026-08-25 --to 2026-08-31 # events in that range of days
```

`spoor ingest` is safe to run as often as you like: importing the same data
twice changes nothing. It is also worth running often — Claude Code deletes
its own session logs after 30 days, and only what has been imported survives.

## Checking that it actually works

Do this once after installing. It takes a couple of minutes and it is the only
way to know the thing is collecting your days rather than pretending to.

**1. It runs.**

```
spoor version
```

**2. The first import.**

```
spoor ingest
```

Expect three lines: files, events, total. A few seconds for a month of
history. There is no fourth line about dropped lines — that one appears only
when something was unreadable, and on healthy data it never does.

**3. The second import, straight after — this is the important one.**

```
spoor ingest
```

Expect `0 files read`, `0 new`, the same total as before, and no measurable
delay. A non-zero `new` is not automatically a bug: if you kept working
between the two runs, Claude Code appended lines in the meantime. Run it a
third time; that one must report zero.

**4. Re-importing does not inflate a week.**

Pick a week entirely in the past — one that can no longer grow — and count it,
import again, count again:

```
spoor count --from 2026-08-25 --to 2026-08-31
spoor ingest --quiet
spoor count --from 2026-08-25 --to 2026-08-31
```

Both numbers must be identical. This is the property the whole design rests
on: a repeated import adds nothing.

**5. Nobody else can read your database.**

```
ls -l ~/.local/share/spoor/
```

Expect `-rw-------` on `spoor.db`. The `-wal` and `-shm` files next to it only
exist while something has the database open, and carry the same permissions.

If you set `SPOOR_DB` or `XDG_DATA_HOME`, the database is not there — the
"Removing everything" section below prints the path it is actually at, and the
same path applies to the next step.

**6. Look at what it collected. Do not skip this one.**

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(*r,sep=' | ') for r in d.execute('SELECT ts,type,subtype,project,raw_text FROM events ORDER BY ts DESC LIMIT 20')]"
```

With the `sqlite3` client installed, the same thing reads better:

```
sqlite3 ~/.local/share/spoor/spoor.db \
  'SELECT ts, type, subtype, project, raw_text FROM events ORDER BY ts DESC LIMIT 20'
```

Now check the one column that could betray the promise at the top of this
file. `raw_text` is the only place a leak could hide, so look at its worst
cases rather than its average:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(repr(r[0])) for r in d.execute('SELECT DISTINCT raw_text FROM events ORDER BY length(raw_text) DESC LIMIT 10')]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  'SELECT DISTINCT raw_text FROM events ORDER BY length(raw_text) DESC LIMIT 10'
```

Every value must be one of: a model name, a comma-separated list of tool
names, `prompt`, `tool_result`, or an attachment kind. Length alone proves
nothing — MCP tool names look like `mcp__server__some_long_tool_name`, and a
message calling three of them makes a long and perfectly innocent label. What
would be a bug is a **sentence**: anything resembling something you typed, a
reply, or the contents of a file. Report that immediately.

**7. An event outlives the file it came from.**

Claude Code erases its logs after 30 days by default, so this property is the
whole reason the database exists. Checked here on a copy of one session, in a
temporary directory — your real logs and your real database are not touched:

```
SPOOR_TMP=$(mktemp -d) && mkdir -p "$SPOOR_TMP/p" && (
  set -e
  cp "$(find ~/.claude/projects -name '*.jsonl' | head -1)" "$SPOOR_TMP/p/"
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p" --quiet
  rm "$SPOOR_TMP/p"/*.jsonl
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p"
); rm -rf "$SPOOR_TMP"
```

The second import sees no files at all and still reports a non-zero
`database: N events total`. The source file is gone; the events are not.

The cleanup is deliberately outside the `&&` chain: the copy is a real
transcript, and it gets removed whether the check succeeded or not.

## Where things live

Everything follows the XDG base directory spec:

| What | Where | Override |
|---|---|---|
| Database | `~/.local/share/spoor/spoor.db` | `SPOOR_DB`, `XDG_DATA_HOME` |
| Config — planned, nothing writes it yet | `~/.config/spoor/` | `XDG_CONFIG_HOME` |
| Claude Code logs (read) | `~/.claude/projects/` | `--claude-dir`, `CLAUDE_CONFIG_DIR` |

The database is meant to be read with your own eyes. No blobs, no serialised
structs. If you cannot see what the tool collected about you, that is a bug.

## Removing everything

`spoor` writes to one directory and nowhere else. It installs no service, no
timer and no shell hook, so removing it is removing files.

**Before you do: the database is the only copy.** Claude Code deletes its own
session logs after 30 days by default, so everything imported from further
back than that exists nowhere else on the machine. Deleting it is not
undoable.

```
rm -rf ~/.local/share/spoor    # the database and its -wal / -shm files
rm -f  ~/.local/bin/spoor      # the binary, wherever you put it
```

There is no configuration yet, so nothing ever creates `~/.config/spoor`. If
you made one by hand, remove that too.

If you set `SPOOR_DB` or `XDG_DATA_HOME`, the database is where those point
instead. Print the real path before deleting anything:

```
echo "${SPOOR_DB:-${XDG_DATA_HOME:-$HOME/.local/share}/spoor/spoor.db}"
```

A custom `SPOOR_DB` names the file, not a directory, so remove its `-wal` and
`-shm` siblings alongside it.

To wipe the collected data but keep using the tool, delete only the database.
The next `spoor ingest` rebuilds it from whatever Claude Code has not yet
erased — the last 30 days by default, and no more.

Nothing in `~/.claude/` is ever written to or deleted by `spoor`; removing it
is Claude Code's business, not this tool's.

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).
