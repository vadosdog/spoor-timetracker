# spoor

Reconstructs the working day after the fact, from traces already on your disk.
Nothing to start, nothing to stop, nothing running in the background — and the
screen is never captured.

Today it reads two sources: the session logs Claude Code writes under
`~/.claude/projects`, and browsing history — of which only Google Chrome is
verified; see [Requirements](#requirements). With neither installed there is
nothing for it to read.

Those session logs are written by anything with a working directory — the CLI,
the editor extension, a Cowork session in the desktop app. A conversation in
the desktop app's plain chat tab writes none of them and is invisible here.
That is not a gap somebody forgot: a chat has no working directory, so there
would be nothing to name the time after even if the time could be found.

**This is a personal tool, published in the open. It works for its author.
There are no guarantees, no support and no promises about the next version.**

## Status

Early. `spoor` imports those two sources into SQLite and reports what a day or
a week went on: projects, hours, and the evidence for each. It cannot yet be
told that a block was something other than what it guessed — naming things by
hand, and an interface to do it in, are still ahead.

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
- **`python3` or the `sqlite3` client** — optional, and only for looking
  inside the database: the inspection step of the check below, and the queries
  under [The config file](#the-config-file). Either one will do, and most
  systems already have one.

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
spoor report [--day[=YYYY-MM-DD] | --week[=YYYY-MM-DD]] [--json | --table]
             [--timeline] [--min 5m] [--gap 10m] [--attention-window 5m]
             [--head 2m] [--tail 2m] [--count-background]
             [--db PATH] [--config PATH]
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

**`report`** reads the database and nothing else. It touches no source and
writes nothing — pointed at a path with no database it says so rather than
creating an empty one — and running it a hundred times changes nothing, which
is the point of collecting and reporting being separate commands.

```
spoor report --day=2026-05-04
```

```
day 2026-05-04 Monday — traces 09:00 to 12:39

PROJECT       ATTENTION  BACKGROUND  AGENT  WALL  ON WHAT GROUNDS
payments-api  1:03       0:23        -      1:22  claude-code 40, browser 10; git.example.com/payments-api 9, docs.example.com/guides 1
checkout-web  0:43       -           0:49   3:39  claude-code 70, browser 5; localhost:3000 5
(no project)  0:14       -           -      0:12  browser 5; www.example.com/search 5; neighbours disagree: checkout-web or payments-api

attention                              2:00  the column above, summed
agent worked in the background         0:23  not counted — pass --count-background to add it
of which written and read              0:10  added by --head and --tail, which are an estimate rather than a measurement
agent worked while you were elsewhere  0:49  summed per project — two agents can be busy in the same second, so this is not part of the day
active                                 2:23  of 3:43 from the first block to the last — 64% covered
```

That day is invented, and so are the domains in it; the shape is what `spoor`
prints.

With no flags it reports on today. `--day` and `--week` also work bare —
`spoor report --week` is the Monday-to-Sunday week you are in. A date goes
with an **equals sign**, because of that: `--day 2026-05-04` written with a
space is refused rather than quietly treated as today, which would look
exactly like the right answer.

`--timeline` adds the same day read the other way round — from when to when,
on what — with the pauses between blocks printed as pauses rather than
skipped:

```
timeline
  08:58-10:24  1:26  payments-api  claude-code 90, browser 10, git.example.com/payments-api, docs.example.com/guides — 0:23 of it the agent alone
  10:24-11:10  0:46  — nothing —   not counted
  11:10-11:24  0:14  (no project)  browser 5, www.example.com/search
  11:24-11:58  0:34  — nothing —   not counted
  11:58-12:41  0:43  checkout-web  claude-code 20, browser 5, localhost:3000
```

A line ends where the project you were on changed. With two or three windows
open that is every few minutes, so a working afternoon is a dozen lines and
not one.

Over a range of days `spoor report --week` normally ends with a `by day`
summary — one line per day, attention and background and the hours its traces
span. `--timeline` replaces that summary with the schedule; the table above it
stays either way.

Projects holding less than `--min` on every count — five minutes by default —
are folded into one line rather than each getting a row. Folded, not dropped:
the line says how many there were, names as many as fit, and carries their
time, so the column still adds up to the total printed under it. A project
with no attention but hours of `agent` is never folded, because that is the
case worth looking at. `--min=0` gives every project a row.

`--json` is not affected by `--min` and prints the same report with every
block and every line of the timeline in it, the full list of browser keys per
project, the settings it ran under, and every duration twice — as `H:MM` and
as milliseconds beside it. Two runs over the same database produce
byte-identical output; there is a test for that, and step 9 below is how you
check it yourself.

### What the numbers mean

**Blocks.** Events no further apart than `--gap` — ten minutes by default —
are one block of work. A pause of exactly the threshold is still one
block; one second more is two. The pauses *between* blocks are not counted at all: `spoor`
does not decide whether your lunch was work, so it does not quietly bill it.

A block reaches a little past its own events at both ends, because writing a
prompt and reading the last answer are real time that leaves no trace:
`--head` before a block that opens with a prompt, `--tail` after a block that
had somebody in it at all, two minutes each by default. Both need a person:
a block of nothing but agent output — a session resumed with its prompt in an
earlier block — gets neither, because there was nobody there to do the writing
or the reading. Neither end may take more than half the pause it reaches into,
so two blocks can never claim the same second.

Unlike the thresholds, these two are somebody's estimate of their own habits
and not a measurement, so the report says how much they added on a line of its
own — `of which written and read`. `--head=0 --tail=0` turns them off and gets
you back to counting only what is on disk.

**Four numbers, deliberately not added together:**

- **attention** — you were at the keyboard, in that project. This is the one
  that gets summed, and the only one that does.
- **background** — the agent kept a block of work going across a pause long
  enough that it would otherwise have ended. Its own line, named for what it
  is. Not counted unless you pass `--count-background`, which adds a `counted`
  line at the top of the summary and changes what is summed, never what is
  measured.
- **agent** — that project's window was working while you were in a different
  one. Per project, and *not* part of the day: two agents can be busy in the
  same second, so this column can add up to more than 24 hours.
- **wall** — first to last event of that project. Context only. Four chats in
  parallel give you four wall times and one day of attention, which is the
  whole reason these are four columns and not one.

A typed prompt and a page your browser recorded are moments of attention; an
assistant message, a tool result and a subagent's prompt are the machine. Each
moment casts a window of `--attention-window` either side of itself, the
windows merge, and active time outside all of them is background.

That window defaults to **half of `--gap`**, and half is the point rather than
a coincidence: at exactly half, two touches leave a gap between them only when
they were further apart than the clustering threshold — the pause that would
have ended the block if the agent had not been filling it. Set it explicitly
and you get a different meaning, not a better one.

**Which project owns a second is decided by the nearest thing you touched**,
not by whichever agent spoke. With three windows open the two you are not in
keep writing, and crediting them with your minutes would hand the day to the
noisiest agent. Nearest in either direction rather than most recent, because a
person reads before they answer: the minute before a prompt was spent on the
thing about to be prompted, not on the thing left behind. And `agent` compares
*windows*, not project names — one chat changes its own working directory
whenever a shell command does, and it is still one chat.

**Where a project comes from.** A Claude Code event names its own, from the
working directory. A browser visit never does. So: a visit takes the project
of the nearest *prompt* in its block — a prompt, not merely the nearest event
carrying a project, for the same reason as above, since otherwise the noisiest
agent gets back in through the browsing. A block holding no prompt at all
falls back to the nearest event that has a project. And a block with *nothing*
named in it — which is most browsing — looks at the nearest named block on
either side:

- both name the same project, or there is only one of them and nothing at all
  on the far side — the block takes that name. A missing neighbour is not a
  disagreement: browsing that opens a morning has nothing before it;
- they name different projects — the block stays `(no project)` and both
  candidates are printed. Choosing between two is the one thing this rule
  will not do.

The one-sided case is the weakest rule here, so it does not hide inside the
total: blocks named that way are `one-neighbour` in `--json`, and the table
adds a line saying how much of the report rests on them.

None of these numbers is settled, which is why every one of them is a flag and
a config key rather than a constant. Ten minutes is where the density of
pauses breaks on two weeks of one person's data, in buckets holding four to
eleven observations each — the best number available rather than a good one.
The window follows it. The two minutes of head and tail have no measurement
under them at all, only somebody's belief about their own habits. Moving any
of them moves the headline number a long way — try `--gap 15m` and see.

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

## The config file

There is no config file until you make one, and `spoor` never writes it. It
holds two sections today — the browser source, and the thresholds the report
is built on:

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

report:
  # Pauses shorter than this are the same block of work.
  cluster_gap: 10m
  # How far either side of a prompt or a page you count as being there.
  # Left out, it follows the gap: half of it.
  attention_window: 5m
  # Writing a prompt, and reading the last answer. Set either to 0s to count
  # only what actually left a trace.
  head: 2m
  tail: 2m
  # Add the agent's own time to the totals. Off by default.
  count_background: false
```

Every key under `report` is also a flag on `spoor report` — `--gap`,
`--attention-window`, `--head`, `--tail`, `--count-background` — and the flag
wins for that run.

All four durations are written the way a person writes one: `10m`, `90s`,
`1h30m`, and a negative one is an error rather than a silent fall back to the
default.

Zero is where they part company. For `cluster_gap` and `attention_window`,
`0s` means the default, exactly as leaving the key out does: a threshold of
nothing would make every event a block of its own, which nobody means by
writing zero, and there would then be no way left to ask for the default. For
`head` and `tail`, `0s` means zero — "add no time I cannot see" has to be
sayable, and it is the whole point of having them configurable. The flags
behave the same way as the keys.

### Ignoring domains

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

**And on a few hosts the secret is the first path segment itself.** Nothing
below it is ever stored, which keeps `/reset-password/<token>` safe — but that
rule does not generalise upwards. `api.telegram.org/bot<token>` puts a bot's
credential in segment one; so do `meet.google.com/<code>`, `forms.gle/<id>`, a
link shortener's slug and a payment QR redirect. `spoor` cannot tell those
from a project name, and the ignore list is the only control there is. Look
for them before the first import:

```
python3 -c "import sqlite3,os;d=sqlite3.connect(os.path.expanduser('~/.local/share/spoor/spoor.db'));[print(r[0],'|',r[1]) for r in d.execute(\"SELECT DISTINCT host, path_head FROM events WHERE length(path_head) >= 20 ORDER BY host\")]"
```

```
sqlite3 ~/.local/share/spoor/spoor.db \
  "SELECT DISTINCT host, path_head FROM events WHERE length(path_head) >= 20 ORDER BY host"
```

Long is not the same as secret — an article slug is long and harmless — but
everything that *is* a secret in that column will be in this list. A host that
turns one up belongs on `ignore`, and the rows already imported have to be
deleted by hand — the two `DELETE` snippets earlier in this section do that,
with the host substituted.

**`history`** adds history databases to the ones found automatically — for a
browser installed somewhere unusual, or one `spoor` has never heard of. A
file named `History` is read as Chrome, one named `places.sqlite` as Firefox.
This is the additive version of `--browser-history`; that flag replaces both
the search and this list for the run it is given on.

A misspelled key anywhere in this file is an error rather than a setting that
silently does nothing — for the reason given above, and because a threshold
that quietly stayed at its default would be just as invisible.

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

**9. The report says the same thing twice.**

Reproducibility is the promise the whole design rests on, and it is one
`cmp` away from being checked rather than believed:

```
spoor report --week --json > /tmp/spoor-1.json
spoor report --week --json > /tmp/spoor-2.json
cmp /tmp/spoor-1.json /tmp/spoor-2.json && echo identical
rm /tmp/spoor-1.json /tmp/spoor-2.json
```

`cmp` prints nothing when two files match, so the only output must be the
word `identical`. Run `spoor ingest` in between and the two will differ, which
is correct: the events changed.

Then read the table for a day you remember:

```
spoor report --day
```

`attention` is never larger than `active`, and `active` is never larger than
the span it is quoted against — those are what the arithmetic guarantees. What
it cannot guarantee is that the projects are yours: `(no project)` is time
nothing could name, and a project appearing twice under two names is
`basename(cwd)` doing what it does until the dictionary arrives.

If a project shows `agent` time and no time of yours at all — neither
attention nor background — the report names it in a paragraph under the
summary. Nothing starts an agent but a person, so that combination means
either something ran unattended or — far more often — one chat window is being
counted under two names, because a shell command that changes directory
changes what the session calls itself.

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
