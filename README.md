# spoor

Reconstructs the working day after the fact, from traces already on your disk.
Nothing to start, nothing to stop, nothing running in the background — and the
screen is never captured.

Today it reads two sources: the session logs Claude Code writes under
`~/.claude/projects`, and browsing history — of which only Google Chrome is
verified; see [Requirements](#requirements). With neither installed there is
nothing for it to read.

**This is a personal tool, published in the open. It works for its author.
There are no guarantees, no support and no promises about the next version.**

## Status

Early. `spoor` can do two things: import those two sources into SQLite, and
count what it stored for a date range. There is no report, no clustering and
no interface yet.

[docs/status.md](docs/status.md) says where the work stopped, and lists the
known rough edges — read it before filing a bug, because the thing you found
may be on it already.

## Roadmap

The order things are meant to happen in, without dates. This is an intention,
not a commitment: the tool is written in the evenings, and the order changes
when the data says something unexpected. It already has once — browser history
was meant to come late, and the measurements moved it up to second, which is
where it now is.

**Next**

- **The report.** Events clustered into blocks, with wall time, attention time
  and agent time kept apart instead of summed. Four agents working in parallel
  must not add up to 32 hours in a day.
- **Projects and rules.** A config mapping paths, branches and domains to
  projects, plus one level below that for something which spans weeks —
  episode 14, level 3, a single feature.
- **Confirming the day.** A terminal interface: walk the blocks, name what the
  rules could not, and have that answer stored as a rule, so the same question
  is not asked twice. Everything it does stays available as flags.

**Later**

- git and file modification times as sources. git earns its place through
  attribution rather than hours: a commit is a point in time, not an interval.
- Sources as external plugins — a date range in, JSON events out — so a source
  nobody else needs can live outside this repository.
- Optional human-readable descriptions of a block. Off by default, local model
  first, and the tool keeps working with the network mode never enabled.
- Export and import, plus a complaint when no import has run for a while.
  Until that exists the caveat under [Uninstall](#uninstall) stands.

Undecided: the calendar. Meetings are the one gap no local trace can fill —
nothing happens on disk during a call — but a calendar lives on the network,
and this tool does not.

## What it never does

- **No network.** The tool never opens a network connection. Not for updates,
  not for telemetry, not at all. If that ever changes, it will be off by
  default and this section will say so, above the install instructions.
- **Metadata only.** Times, paths, branches, model names, tool names, the
  identifiers Claude Code puts on its own sessions and lines, and the address
  fields described in the next point. The text of your conversations, of tool
  results and of attachments is never stored — a boundary of the project, not
  a setting. Step 6 below shows you every column there is, so you can check
  that instead of trusting this paragraph.
- **No URLs.** From your browsing history `spoor` keeps the host, the port,
  the *first* path segment, the page title and the time. Never the query
  string, never the fragment, never a second path segment — that is where
  session tokens, password-reset links and search terms usually live. Usually,
  not always: on `forms.gle` or `meet.google.com` the first segment *is* the
  identifier, and no rule about characters can tell one of those from a
  project name. Page titles are kept too, and a search results page has your
  query in its title. For both, the control is
  [Ignoring domains](#ignoring-domains).
- **Read-only where your other programs are concerned.** `spoor` never writes
  to or deletes anything under `~/.claude/`, and it never opens a browser's
  history *as a database*: it reads the file through once, byte for byte,
  into a private copy, works on the copy and deletes it. No lock is taken and
  nothing is written back, whether or not the browser is running. Step 8
  below is how you check that.
- **No cgo.** SQLite is `modernc.org/sqlite`, so one static binary
  cross-compiles anywhere.

## Requirements

- **Go 1.25 or newer**, to build. Nothing is needed at runtime: the binary is
  self-contained.
- **At least one source.** Claude Code, with session logs in
  `~/.claude/projects`; or a browser — see below. Any source that is not
  there is skipped without complaint.
- **`python3` or the `sqlite3` client** — optional, and only for the
  inspection step of the check below. Either one will do, and most systems
  already have one.

**Of the browsers, only Google Chrome on Linux has been run against a real
profile.** That is the whole of what is verified. The Firefox reader is
written against the published schema and tested on invented databases, and
has never seen a real `places.sqlite`. Anything else — Chromium, Brave, Edge,
Vivaldi, a Firefox fork — reads the same two file formats and may well work
if you point `spoor` at the file, but none of it has been tried and none of
it is supported. If it does not work, that is expected rather than a bug.

Built and used on Ubuntu, with Chrome and Claude Code. That combination is the
whole of what is verified. CI cross-compiles for linux/amd64, linux/arm64,
darwin/arm64 and windows/amd64, but a build that succeeds is not a claim that
anything works there: macOS and Windows have never been run, including where
the browser source looks for profiles on them.

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
spoor ingest [--db PATH] [--config PATH] [--claude-dir PATH]
             [--browser-history PATH] [--no-claude-code] [--no-browser] [--quiet]
spoor count  [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--source NAME] [--db PATH]
spoor version
```

`spoor <command> -h` prints the flags of that command.

**`ingest`** imports what is new and leaves the rest alone: importing the same
data twice changes nothing. Run it regularly — the sources erase themselves,
and only what has been imported survives that. See
[Uninstall](#uninstall) for how long each one keeps.

`--browser-history` names a history database to read *instead of* searching
the usual places, and may be given more than once. It also replaces the
config's `history` list, not just the search. Which browser a file belongs to
is decided by its name: `History` is read as Chrome, `places.sqlite` as
Firefox. Pointing it at some other browser's file of the same name is
possible and untested — see [Requirements](#requirements).

A history file you name and `spoor` cannot use — wrong name, or not there —
is a warning naming the file, not a silent skip and not a failed run.

**`count`** defaults to the last seven days, ending today. `--source` takes
`claude-code` or `browser`. Days are local days: timestamps are stored in UTC,
and the local day boundaries you give it are converted to UTC when querying.

**`version`** prints `dev` unless the binary was built with `just build`, which
stamps the version from git.

## Where things live

Everything follows the XDG base directory spec:

| What | Where | Override |
|---|---|---|
| Database | `~/.local/share/spoor/spoor.db` | `SPOOR_DB`, `XDG_DATA_HOME` |
| Config, optional — nothing writes it | `~/.config/spoor/config.yaml` | `--config`, `SPOOR_CONFIG`, `XDG_CONFIG_HOME` |
| Claude Code logs, never written to | `~/.claude/projects/` | `--claude-dir`, or `CLAUDE_CONFIG_DIR` (read as `$CLAUDE_CONFIG_DIR/projects`) |
| Chrome profiles, copied not opened | `~/.config/google-chrome/*/History` | `--browser-history` |
| Firefox profiles, copied not opened | `~/.mozilla/firefox/*/places.sqlite` | `--browser-history` |

Flatpak installations of either browser are searched for as well, and Snap
for Firefox. Nothing else is searched for — not Snap Chrome, not Chromium,
Brave, Edge or Vivaldi. Those can be named by hand with `--browser-history`
or the `history` key in the config, with the caveat in
[Requirements](#requirements).

On macOS and Windows the documented profile locations are used; those are the
ones nobody has verified.

If `SPOOR_DB` or `XDG_DATA_HOME` is set, this prints where the database
actually is:

```
echo "${SPOOR_DB:-${XDG_DATA_HOME:-$HOME/.local/share}/spoor/spoor.db}"
```

The commands below spell out the default path. Substitute the one above if
yours differs.

## Ignoring domains

There is no config file until you make one, and `spoor` never writes it. The
only thing it holds today is the browser source:

```yaml
# ~/.config/spoor/config.yaml
browser:
  ignore:
    - videos.example
    - social.example
  history:
    # A Chrome profile somewhere the search does not look — a second install,
    # a restored backup. Another browser's file can be named here too, with
    # the caveat under Requirements: untried, and not supported.
    - /mnt/backup/google-chrome/Profile 1/History
```

**`ignore`** is a list of domains that are never imported. An entry covers the
host itself and every subdomain of it, so `example.com` also covers
`www.example.com`. The comparison starts at a dot, so `notexample.com` is a
different domain and stays.

An ignore list that quietly does nothing is the worst thing this file could
do, so two things are said out loud. An entry that could never match a host —
`localhost:3000`, `*.example.com`, `example.com/watch` — is rejected with a
warning; ports and wildcards are not needed, since an entry already covers
every subdomain.

And an entry holding a character you cannot see is kept, but named: a domain
pasted out of a rendered page or a PDF can carry a zero-width space, and a
soft hyphen is what a word processor puts at a line break. The warning prints
the entry with that character spelled out — `"videos\u200b.example"` — so you
can see which one to delete, and says what it is matching instead.

An address works too. `::1` and `192.0.2.10` are ordinary entries, and any
spelling of one is accepted, so `::1` and `0:0:0:0:0:0:0:1` are the same
entry. **An address matches exactly**: it has no subdomains and no prefixes,
so `10` does not cover `192.0.2.10` — and because `10` is a legal host name,
nothing warns you about it.

If you write the brackets a URL would have, quote them: `- "[::1]"`.
Unquoted, `[` starts a list in YAML, and the whole run stops with a parser
error rather than skipping the entry.

The filter runs **before the insert**, not before the display: an ignored
domain is not stored at all. The other side of that is that it only applies to
what has not been imported yet. A domain added to the list today leaves
yesterday's rows exactly where they are, and removing them is a `DELETE` you
run yourself:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));print(d.execute(\"DELETE FROM events WHERE source='browser' AND (host='videos.example' OR host LIKE '%.videos.example')\").rowcount,'rows');d.commit()"
```

```
sqlite3 ~/.local/share/spoor/spoor.db "DELETE FROM events WHERE source = 'browser' AND (host = 'videos.example' OR host LIKE '%.videos.example')"
```

So it is worth writing the list before the first import rather than after.

**Page titles are stored, and that is what the list is really for.** A search
results page carries your query in its title, and there is no rule that can
tell a work search from a private one. If a domain's titles are nobody's
business, the domain belongs on this list.

**`history`** adds history databases to the ones found automatically — for a
browser installed somewhere unusual, or one `spoor` has never heard of. A
file named `History` is read as Chrome, one named `places.sqlite` as Firefox;
whether some other browser's file of that name actually reads is untested.
This is the additive version of `--browser-history`; that flag replaces both
the search and this list for the run it is given on.

A misspelled key is an error rather than a setting that silently does nothing:
an ignore list that is quietly not applied is the worst thing this file could
do.

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

Expect two lines per source it found, then the total — five lines with both
Claude Code and a browser installed, in this shape:

```
claude-code: 120 files (120 read, 0 unchanged), 20000 lines
  events: 14000 found, 14000 new, 6000 session-state lines skipped
browser: 1 profiles (1 read, 0 unchanged), 9000 visits
  events: 8800 found, 8800 new, 0 ignored, 200 not http(s)
database: 22800 events total
```

The numbers above are made up; yours will be your own. A month of session
logs and 90 days of browsing take a second or two together. Extra lines
appear only when something was unreadable, and **a source that found nothing
to read is not mentioned at all** — with only one of the two installed you get
three lines, not five, and that is not a fault.

**3. The second import, straight after — this is the important one.**

```
spoor ingest
```

Expect `(0 read, N unchanged)`, `0 new`, the same total as before, and no
measurable delay — on every source line you got in step 2. A non-zero `new`
is not necessarily a bug: if you kept working — or kept browsing — between
the two runs, there was something new to import. Run it a third time; that
one must report zero.

With the browser open, the browser line is the exception. Chrome and Firefox
write to their history constantly, so that profile counts as changed and is
read again: expect `(1 read, …)` and a large `visits` count with `0 new`.
That is the design, not a wasted pass — a run that reads a changed history
starts a week behind where it stopped and throws away what it already has,
because a visit your phone synced in yesterday carries yesterday's timestamp
and would otherwise be behind the mark forever. Close the browser and run it
again if you want to see the `0 read` case.

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
import bookkeeping: paths, sizes, read offsets and how far each browser
profile has been read, no event content. The shorter queries below only trim
the view; they hide nothing.

Now the readable summary:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(*r,sep=' | ') for r in d.execute('SELECT ts,source,subtype,project,raw_text,host,port,path_head FROM events ORDER BY ts DESC LIMIT 20')]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  'SELECT ts, source, subtype, project, raw_text, host, port, path_head FROM events ORDER BY ts DESC LIMIT 20'
```

**Check that no address survived whole.** `host`, `port` and `path_head` are
supposed to be the *only* pieces of a URL in there. `path_head` keeps only
what a URL's own grammar allows in one path segment, less the characters that
attach a parameter to a name, and stops at the first thing that is not. The
query below spot-checks the ones that would matter most — a `?`, a `#`, a
`;`, a `\` or a `://`. The semicolon and the backslash are there because
`/portal;jsessionid=…` is how a session token ends up inside a path segment,
and `\` is the path separator on a Windows server:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));print(d.execute(\"SELECT count(*) FROM events WHERE host||port||path_head LIKE '%?%' OR host||port||path_head LIKE '%#%' OR host||port||path_head LIKE '%;%' OR instr(host||port||path_head, char(92)) > 0 OR host||port||path_head LIKE '%://%'\").fetchone()[0])"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  "SELECT count(*) FROM events WHERE host||port||path_head LIKE '%?%' OR host||port||path_head LIKE '%#%' OR host||port||path_head LIKE '%;%' OR instr(host||port||path_head, char(92)) > 0 OR host||port||path_head LIKE '%://%'"
```

The answer must be `0`.

A replacement character — `�`, where a letter should be — is not a bug.
What a browser stores as a path is bytes rather than text, and a site older
than UTF-8 encodes its path in the page's own charset, so `/caf%E9/` arrives
as a byte no UTF-8 decoder accepts. `spoor` replaces those, because SQLite
will store such a byte and then refuse to hand the column back at all — which
would make the queries above print nothing rather than something wrong.

**`raw_text` and `title` are the two columns where a leak could hide**, so
look at their worst cases rather than their average:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(repr(r[0])) for r in d.execute('SELECT DISTINCT raw_text FROM events ORDER BY length(raw_text) DESC LIMIT 10')]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  'SELECT DISTINCT raw_text FROM events ORDER BY length(raw_text) DESC LIMIT 10'
```

Every `raw_text` must be one of: empty, a model name, a comma-separated list
of tool names, a model name and tool names separated by a space, `prompt`,
`tool_result`, or the kind of an attachment.

Length proves nothing on its own — MCP tool names look like
`mcp__server__some_long_tool_name`, so a message that called three of them
makes a long and perfectly innocent label. An MCP server name can still say
where you work, so mind that before showing the database to anyone. What would
be a bug is a **sentence**: anything resembling something you
typed, a reply, or the contents of a file.

There is no support, but a sentence in `raw_text` is a leak, and that is worth
an issue. [Open one](https://github.com/vadosdog/spoor-timetracker/issues).

`title` is different, and it is the one to look at hardest, because sentences
there are *not* a bug — a page title is a sentence, and storing it is what
the browser source is for:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(r[0],'|',repr(r[1])) for r in d.execute(\"SELECT host, title FROM events WHERE title <> '' ORDER BY ts DESC LIMIT 30\")]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  "SELECT host, title FROM events WHERE title <> '' ORDER BY ts DESC LIMIT 30"
```

Every search you ran is in that list, in the title of the results page, and
no rule can tell a work search from a private one. That is what
[Ignoring domains](#ignoring-domains) is for. Read the list and decide which
domains you would rather not have; there is no wrong answer, and the ones you
do not want should have been on the list before the first import.

**7. An event outlives the file it came from.**

This check runs on a copy of one session, in a temporary directory: your real
logs and your real database are never touched.

```
SPOOR_TMP=$(mktemp -d) && mkdir -p "$SPOOR_TMP/p" && (
  set -e
  cp "$(find ~/.claude/projects -name '*.jsonl' | head -1)" "$SPOOR_TMP/p/"
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p" --no-browser --quiet
  rm "$SPOOR_TMP/p"/*.jsonl
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p" --no-browser
); [ -n "$SPOOR_TMP" ] && rm -rf "$SPOOR_TMP"
```

The second import prints one line and nothing else — the source found no
files, so per step 2 it says nothing at all — and that line is the same
non-zero `database: N events total` as the first import. The source file is
gone; the events are not.

`--no-browser` is not decoration: without it this throwaway database would
fill up with your real browsing history, which is not what a five-second
check should do.

The cleanup sits outside the `&&` chain on purpose: the copy is a real
transcript, and it has to go whether the check passed or not.

If `CLAUDE_CONFIG_DIR` is set, replace `~/.claude/projects` in the snippet
with `$CLAUDE_CONFIG_DIR/projects`.

**8. Your browser's history is not touched.**

```
ls -l --time-style=full-iso ~/.config/google-chrome/*/History
spoor ingest --quiet
ls -l --time-style=full-iso ~/.config/google-chrome/*/History
```

The size and timestamp must be identical: `spoor` reads the file into a copy
and works on the copy, so it takes no lock and writes nothing back. On
Firefox, substitute `~/.mozilla/firefox/*/places.sqlite`.

Do this with the browser closed. With it running, the browser can change its
own history between the two `ls` calls, and then the check tells you about
the browser rather than about `spoor`.

## Uninstall

`spoor` writes to one directory and nowhere else. It installs no service, no
timer and no shell hook, so removing it is removing files.

**Before you do: the database is the only copy.** Claude Code deletes its own
session logs after 30 days by default and Chrome keeps 90 days of history, so
everything imported from further back than that exists nowhere else on the
machine. There is no undo, and there is no export either — if the history
matters to you, `spoor.db` belongs in whatever backup you already run.

```
rm -rf ~/.local/share/spoor    # the database and its -wal / -shm files
rm -f  ~/.local/bin/spoor      # the binary, wherever you put it
```

If `SPOOR_DB` or `XDG_DATA_HOME` is set, the database is not there — print the
real path with the `echo` above before deleting anything. A custom `SPOOR_DB`
names the file rather than a directory, so remove its `-wal` and `-shm`
siblings alongside it.

Nothing creates `~/.config/spoor`; if you made one by hand, remove that too.

To wipe the collected data but keep using the tool, delete only the database.
The next `spoor ingest` rebuilds it from whatever the sources have not erased
yet — 30 days of session logs, 90 days of Chrome history — and no further
back.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
