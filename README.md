# spoor

Reconstructs the working day after the fact, from traces already on your disk.
Nothing to start, nothing to stop, nothing running in the background — and the
screen is never captured.

Today it reads one source: the session logs Claude Code writes under
`~/.claude/projects`. Without Claude Code installed there is nothing for it to
read.

**This is a personal tool, published in the open. It works for its author.
There are no guarantees, no support and no promises about the next version.**

## Status

Early. `spoor` can do two things: import Claude Code session logs into SQLite,
and count what it stored for a date range. There is no report, no clustering
and no interface yet.

[docs/status.md](docs/status.md) says where the work stopped, and lists the
known rough edges — read it before filing a bug, because the thing you found
may be on it already.

## What it never does

- **No network.** The tool never opens a network connection. Not for updates,
  not for telemetry, not at all. If that ever changes, it will be off by
  default and this section will say so, above the install instructions.
- **Metadata only.** Times, paths, branches, model names, tool names, and the
  identifiers Claude Code puts on its own sessions and lines. The text of your
  conversations, of tool results and of attachments is never stored — a
  boundary of the project, not a setting. Step 6 below shows you every column
  there is, so you can check that instead of trusting this paragraph.
- **Read-only where Claude Code is concerned.** `spoor` never writes to or
  deletes anything under `~/.claude/`.
- **No cgo.** SQLite is `modernc.org/sqlite`, so one static binary
  cross-compiles anywhere.

## Requirements

- **Go 1.25 or newer**, to build. Nothing is needed at runtime: the binary is
  self-contained.
- **Claude Code**, with session logs in `~/.claude/projects`. That is the only
  source so far.
- **`python3` or the `sqlite3` client** — optional, and only for the
  inspection step of the check below. Either one will do, and most systems
  already have one.

Built and used on Linux. CI cross-compiles for linux/amd64, linux/arm64,
darwin/arm64 and windows/amd64, but macOS and Windows are unverified beyond
the fact that they compile.

## Install

There are no releases yet; build it yourself.

```
git clone https://github.com/vadosdog/spoor-timetracker
cd spoor-timetracker
go build -o ~/.local/bin/spoor ./cmd/spoor
```

`~/.local/bin` is just one option — anywhere on your `PATH` works. The rest of
this file assumes `spoor` is runnable by name.

## Commands

```
spoor ingest [--db PATH] [--claude-dir PATH] [--quiet]
spoor count  [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--source NAME] [--db PATH]
spoor version
```

`spoor <command> -h` prints the flags of that command.

**`ingest`** imports what is new and leaves the rest alone: importing the same
data twice changes nothing. Run it regularly — Claude Code deletes its own
session logs after 30 days by default, and only what has been imported
survives that.

**`count`** defaults to the last seven days, ending today. Days are local
days: timestamps are stored in UTC, and the local day boundaries you give it
are converted to UTC when querying.

**`version`** prints `dev` unless the binary was built with `just build`, which
stamps the version from git.

## Where things live

Everything follows the XDG base directory spec:

| What | Where | Override |
|---|---|---|
| Database | `~/.local/share/spoor/spoor.db` | `SPOOR_DB`, `XDG_DATA_HOME` |
| Config — planned, nothing writes it yet | `~/.config/spoor/` | `XDG_CONFIG_HOME` |
| Claude Code logs, read only | `~/.claude/projects/` | `--claude-dir`, or `CLAUDE_CONFIG_DIR` (read as `$CLAUDE_CONFIG_DIR/projects`) |

If `SPOOR_DB` or `XDG_DATA_HOME` is set, this prints where the database
actually is:

```
echo "${SPOOR_DB:-${XDG_DATA_HOME:-$HOME/.local/share}/spoor/spoor.db}"
```

The commands below spell out the default path. Substitute the one above if
yours differs.

## Check it works

Do this once after installing; it takes a couple of minutes.

**1. It runs.**

```
spoor version
```

**2. The first import.**

```
spoor ingest
```

Expect three lines: files, events, total. A month of history takes a few
seconds. A fourth line appears only when something was unreadable.

**3. The second import, straight after — this is the important one.**

```
spoor ingest
```

Expect `(0 read, N unchanged)` — N being however many session files you have —
then `0 new`, the same total as before, and no measurable delay. A non-zero `new` is not necessarily a bug: if you kept
working between the two runs, Claude Code appended lines in the meantime. Run
it a third time; that one must report zero.

**4. Re-importing does not inflate a week.**

Pick a week entirely in the past — one that can no longer grow — then count
it, import again, and count again:

```
spoor count --from 2026-08-25 --to 2026-08-31
spoor ingest --quiet
spoor count --from 2026-08-25 --to 2026-08-31
```

Both numbers must be identical.

**5. Nobody else can read your database.**

```
ls -l ~/.local/share/spoor/
```

Expect `-rw-------` on `spoor.db`. The `-wal` and `-shm` files next to it exist
only while a process has the database open, and carry the same permissions.

**6. Look at what it collected. Do not skip this one.**

Start with whole rows, every column, nothing left out of the view:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));c=d.execute('SELECT * FROM events ORDER BY ts DESC LIMIT 3');n=[x[0] for x in c.description];[print('\n'.join(f'{k}: '+repr(v) for k,v in zip(n,r)),end='\n\n') for r in c]"
```

```
sqlite3 -line ~/.local/share/spoor/spoor.db \
  'SELECT * FROM events ORDER BY ts DESC LIMIT 3'
```

That is the whole record. No other table holds event data — `source_files` is
import bookkeeping: paths, sizes and read offsets, no event content. The
shorter queries below only trim the view; they hide nothing.

Now the readable summary:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(*r,sep=' | ') for r in d.execute('SELECT ts,type,subtype,project,raw_text FROM events ORDER BY ts DESC LIMIT 20')]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  'SELECT ts, type, subtype, project, raw_text FROM events ORDER BY ts DESC LIMIT 20'
```

`raw_text` is the one column where a leak could hide, so look at its worst
cases rather than its average:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(repr(r[0])) for r in d.execute('SELECT DISTINCT raw_text FROM events ORDER BY length(raw_text) DESC LIMIT 10')]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  'SELECT DISTINCT raw_text FROM events ORDER BY length(raw_text) DESC LIMIT 10'
```

Every value must be one of: empty, a model name, a comma-separated list of
tool names, a model name and tool names separated by a space, `prompt`,
`tool_result`, or the kind of an attachment.

Length proves nothing on its own — MCP tool names look like
`mcp__server__some_long_tool_name`, so a message that called three of them
makes a long and perfectly innocent label. An MCP server name can still say
where you work, so mind that before showing the database to anyone. What would
be a bug is a **sentence**: anything resembling something you
typed, a reply, or the contents of a file.

There is no support, but a sentence in `raw_text` is a leak, and that is worth
an issue. [Open one](https://github.com/vadosdog/spoor-timetracker/issues).

**7. An event outlives the file it came from.**

This check runs on a copy of one session, in a temporary directory: your real
logs and your real database are never touched.

```
SPOOR_TMP=$(mktemp -d) && mkdir -p "$SPOOR_TMP/p" && (
  set -e
  cp "$(find ~/.claude/projects -name '*.jsonl' | head -1)" "$SPOOR_TMP/p/"
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p" --quiet
  rm "$SPOOR_TMP/p"/*.jsonl
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p"
); [ -n "$SPOOR_TMP" ] && rm -rf "$SPOOR_TMP"
```

The second import sees no files at all and still reports a non-zero
`database: N events total`. The source file is gone; the events are not.

The cleanup sits outside the `&&` chain on purpose: the copy is a real
transcript, and it has to go whether the check passed or not.

If `CLAUDE_CONFIG_DIR` is set, replace `~/.claude/projects` in the snippet
with `$CLAUDE_CONFIG_DIR/projects`.

## Uninstall

`spoor` writes to one directory and nowhere else. It installs no service, no
timer and no shell hook, so removing it is removing files.

**Before you do: the database is the only copy.** Claude Code deletes its own
session logs after 30 days by default, so everything imported from further
back than that exists nowhere else on the machine. There is no undo, and there
is no export either — if the history matters to you, `spoor.db` belongs in
whatever backup you already run.

```
rm -rf ~/.local/share/spoor    # the database and its -wal / -shm files
rm -f  ~/.local/bin/spoor      # the binary, wherever you put it
```

If `SPOOR_DB` or `XDG_DATA_HOME` is set, the database is not there — print the
real path with the `echo` above before deleting anything. A custom `SPOOR_DB`
names the file rather than a directory, so remove its `-wal` and `-shm`
siblings alongside it.

Nothing creates `~/.config/spoor` yet; if you made one by hand, remove that
too.

To wipe the collected data but keep using the tool, delete only the database.
The next `spoor ingest` rebuilds it from whatever Claude Code has not erased
yet, and no further back.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
