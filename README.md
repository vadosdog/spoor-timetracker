# spoor

Reconstructs the working day after the fact, from traces already on your disk.
Nothing to start, nothing to stop, nothing running in the background — and the
screen is never captured.

Today it reads three sources: the session logs Claude Code writes under
`~/.claude/projects`, browsing history — of which only Google Chrome is
verified; see [Requirements](#requirements) — and, if you switch it on, a
calendar. With none of them there is nothing for it to read.

The calendar is the one source that leaves your machine, and it is off until
you turn it on. [Network](#network) says exactly what it does and what it
costs you; read that section before the install instructions, not after.

Those session logs are written by anything with a working directory — the CLI,
the editor extension, a Cowork session in the desktop app. A conversation in
the desktop app's plain chat tab writes none of them and is invisible here.
That is not a gap somebody forgot: a chat has no working directory, so there
would be nothing to name the time after even if the time could be found.

**This is a personal tool, published in the open. It works for its author.
There are no guarantees, no support and no promises about the next version.**

## Status

Early. `spoor` imports those sources into SQLite and reports what a day or
a week went on: projects, hours, and the evidence for each. Which project
something belongs to is decided by a dictionary you write in the config file:
directory paths, browser keys, git branches and page titles. Below a project
sits one more level, for the thing that spans weeks — an episode, a level, a
feature, a ticket.

`spoor confirm` walks a day in the terminal: it asks about what the rules could
not name, takes the answer as a line of the dictionary so the same question is
not asked twice, and freezes the result so that day's numbers stop moving.
Everything it does is also a flag.

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

- git and file modification times as sources. git earns its place through
  attribution rather than hours: a commit is a point in time, not an interval.

**Done since this list was written**

- **Confirming a day.** `spoor confirm` — the terminal interface, the answers
  written back as rules, and the frozen snapshot a report reads instead of
  recomputing. `spoor export` writes a confirmed day out as markdown.
- **The calendar**, as a source. It reads a private iCalendar feed, or a
  downloaded `.ics`, and turns meetings into intervals of the day. The network
  call is explicit and off by default: see [Network](#network).

**Later**

- Sources as external plugins — a date range in, JSON events out — so a source
  nobody else needs can live outside this repository.
- Optional human-readable descriptions of a block. Off by default, local model
  first, and the tool keeps working with the network mode never enabled.
- Exporting and re-importing the **database**, plus a complaint when no import
  has run for a while. (`spoor export` above writes a day out for a person to
  read; this is the other kind.)
  Until that exists the caveat under [Uninstall](#uninstall) stands.

Not planned: a Jira or Confluence integration. Both are used through the
browser, which this already reads, and an API would buy exact edit times and
issue statuses that the report does not need. That changes only if worklogs
ever need writing *back*, which is a different thing entirely.

## What it never does

- **No network unless you ask for it.** Nothing here phones home: no updates,
  no telemetry, no analytics, ever. There is exactly one outbound request the
  tool can make, and only after you write two settings and put a URL in a file
  yourself — fetching your own calendar feed. Out of the box it makes none.
  [Network](#network) is the whole story, and `spoor ingest --no-calendar`
  turns it off regardless of what any config file says.
- **Metadata only.** Times, paths, branches, model names, tool names, the
  identifiers Claude Code puts on its own sessions and lines, a meeting's
  title and how long it ran, and the address fields described in the next
  point. The text of your conversations, of tool results and of attachments
  is never stored — a boundary of the project, not
  a setting. Step 6 below prints every column of every table there is, most of
  them with the comment saying what they hold, so you can check that instead of
  trusting this paragraph.
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

## Network

Meetings are the one hole no local trace can fill. Nothing is written to your
disk while you sit in a call, so a day full of them reads as a day of doing
nothing. The calendar source closes that hole, and it is the only part of
`spoor` that opens a socket.

**It is off.** With no config file, or with a config file that does not
mention a calendar, `spoor` makes no outbound request of any kind. Turning it
on takes three deliberate steps: setting `calendar.enabled: true`, listing a
calendar, and writing that calendar's address into a file by hand. Miss any
one and nothing goes out.

To turn it off again for a single run, whatever the config says:

```
spoor ingest --no-calendar
```

### What is sent, and to whom

One HTTPS GET per calendar, to the address you put in the file, each time
`ingest` runs — plus up to two more if that server answers with a redirect.
Nothing is uploaded, and nothing goes anywhere except the address you
configured and whatever it redirects to; the `Referer` header is stripped on
the way, so the address itself is never handed on. If you point it at a file on
disk instead — `file:` rather than `url_file:` — nothing is sent at all, and
the source works exactly the same.

### Treat the URL as a password

A private iCalendar address is a bearer credential. Anyone holding it can read
your whole calendar — every title, description, attendee, location and
conference link — without logging in as anybody. It does not expire, it cannot
be narrowed, and the only way to revoke it is to reset it, which breaks every
other subscription to that calendar at the same time.

So `spoor` keeps it the way `ssh` keeps a key: on its own, in its own file,
never in the config.

```
~/.config/spoor/
  config.yaml       ← shown in issues, committed to dotfiles. No secrets here.
  calendars/work    ← the URL, one line, mode 0600
```

One command creates it, with the right permissions, reading the URL without
showing it:

```
spoor add-calendar work
```

It asks you to paste the URL, writes it to `~/.config/spoor/calendars/work`
with mode 0600, and prints the config lines to add. It does not edit your
config file — `spoor` has never written that file and is not going to start.

There is deliberately **no flag** for the URL, and `add-calendar` will not take
it as an argument either. On Linux `/proc/<pid>/cmdline` is world-readable by
default, so anything on a command line is visible to every other account on the
machine and lands in your shell history besides.

`spoor` never prints the secret part of the URL. That is deliberate work
rather than a default: Go's own error text embeds the whole URL, so one flaky
connection would otherwise put your credential into a terminal, a screenshot
or a pasted issue. Network errors are reported by cause instead — "connection
refused", "certificate has expired" — with the path removed. Redirects are
followed with the `Referer` header stripped, for the same reason: it would
hand the address to the next host.

The host is not removed, because "no such host" is worth reading and naming the
server is how you know which one failed. For a hosted calendar the secret is
entirely in the path, so that costs nothing; if your feed is self-hosted, the
host names your server.

If the URL does leak, reset it in your calendar's own settings. In Google
Calendar that is *Settings → the calendar → Integrate calendar → Reset*.

### What it stores

Start, end and title. That is all.

Attendees are read in exactly one place — to work out whether *you* declined an
invitation — and are never written anywhere. The description, the location, the
organiser and the conference link are not read at all. Other people's names and
addresses are not yours to collect, and this project stores metadata only.

The title is kept, and it is the field to think about: a meeting called
"1:1 with Sam" puts Sam's name in your database. It is the same trade as page
titles from your browser, and it has the same control — leave the calendar out,
or point it at a calendar you are happy to have on disk.

### What it skips, and why you should look at the count

`ingest` prints a line saying what it threw away:

```
  skipped: 3 cancelled, 12 all-day, 1 marked free, 2 declined, 0 with no length, 0 longer than a day, 0 not expanded, 0 cut short, 0 unreadable
```

Cancelled meetings, meetings you declined, and anything the calendar itself
marks as "free" are not time you spent. All-day entries — holidays, birthdays,
"on leave" — are skipped too, because importing one as a twenty-four hour block
would swallow the whole day, and so is any timed entry running longer than a
day, which is a label on a stretch of dates rather than an hour you sat
through. If that count looks too big, that is the number telling you so.

A timed entry with no length at all is a reminder rather than an interval, and
is skipped for that reason.

The last three are different from the rest, and from each other. They are not
the calendar saying a meeting did not happen — they are spoor failing to read
one, which is why they are counted apart and why the next paragraph matters.

`not expanded` is a repeating meeting whose repetition rule this version will
not guess at: the entry is fine, the whole series is simply absent. `cut short`
is a repeating meeting spoor did start expanding and stopped, because the
series hit one of the limits on how many occurrences one entry may produce —
the meetings before the limit are there, the ones after it are not. And
`unreadable` is an entry this parser could not use at all — a date it cannot
read, a missing or absurd identifier, a rule that would remove occurrences.

Any of the three costs only that entry; the rest of the feed is still imported,
and the reason is printed on a `warning:` line beside the counts. What they
also do is stop the import from taking meetings *away*: a calendar restates its
window on every run, so a run that did not understand the whole of what it was
given deletes nothing at all, and says so instead.

A feed that cannot be read at all — the download stopped early, the server
sent a login page instead, the address was reset — is refused whole rather than
half-imported. It shows up as `1 calendars (0 read)` with a `warning:` line
saying which calendar and why. The run still finishes and the other sources
still import; that calendar simply contributed nothing, and `(0 read)` is the
signal to look for.

Repeating meetings are expanded into the individual occurrences, so a weekly
sync is fifty-two entries in a year rather than one.

## Requirements

- **Go 1.25 or newer**, to build. Nothing is needed at runtime: the binary is
  self-contained.
- **At least one source.** Claude Code, with session logs in
  `~/.claude/projects`; or a browser — see below; or a calendar, which is
  off until you turn it on. See [Network](#network). Any source that is not
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
             [--browser-history PATH] [--no-claude-code] [--no-browser]
             [--no-calendar] [--calendar-back 9600h] [--calendar-forward 744h]
             [--quiet]
spoor report [--day[=YYYY-MM-DD] | --week[=YYYY-MM-DD]] [--json | --table]
             [--timeline] [--subject NAME] [--unmatched]
             [--min 5m] [--gap 10m] [--attention-window 5m]
             [--head 2m] [--tail 2m] [--count-background] [--recompute]
             [--db PATH] [--config PATH]
spoor count  [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--source NAME] [--db PATH]
spoor confirm [--day[=YYYY-MM-DD]] [--questions [--json]] [--yes]
              [--reconfirm] [--list] [--count-background]
              [--windows [--json]]
              [--window HH:MM-HH:MM --worked true|false
               [--window-project NAME [--window-subject NAME]]]
              [--db PATH] [--config PATH]
spoor assign  [--day[=YYYY-MM-DD]]
              (--trace KIND:VALUE | --from HH:MM --to HH:MM --one-off)
              [--project NAME] [--subject NAME] [--never] [--ambiguous]
              [--title EXPR] [--since] [--work true|false] [--dry-run]
              [--db PATH] [--config PATH]
spoor export  [--day[=YYYY-MM-DD]] [--format markdown] [--out PATH]
              [--timeline] [--unconfirmed] [--db PATH] [--config PATH]
spoor add-calendar <id>
spoor version
```

`spoor <command> -h` prints the flags of that command.

**`ingest`** imports what is new and leaves the rest alone: importing the same
data twice changes nothing. Run it regularly — the two local sources erase
themselves, and only what has been imported survives that. See
[Uninstall](#uninstall) for how long each one keeps. A calendar feed does not erase
itself and does not only grow: it restates. So the calendar is the one source
that replaces what it said last time rather than adding to it, and a meeting
that was moved, renamed or cancelled is corrected on the next run. `ingest`
prints how many were moved and how many are no longer in the feed.

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

Once any rule produces a subject, the day and the week grow one more section
under the summary — the accumulating things the range touched, and which
project each fell under:

```
subjects — inside the projects above, not extra to them
  PAY-31  0:06  -  payments-api
```

Attention, then background, then the project. It is not a breakdown of the
table above it and does not add up to it: most of a project's time belongs to
no subject in particular. Eight lines, then a count of the rest.

`--subject NAME` answers a different question: not what a day went on, but how
long one thing has taken altogether. It reads the whole database unless
`--day` or `--week` narrows it, because the thing it asks about — an episode, a
feature, a ticket — has no date range of its own.

```
spoor report --subject PAY-31
```

```
subject "PAY-31" — 2026-05-04 to 2026-05-06, 3 days with traces

PROJECT       ATTENTION  BACKGROUND  EVENTS
payments-api  0:18       -           6

attention                       0:18  the column above, summed over every day this subject appears on
agent worked in the background  -     not counted — pass --count-background to add it
events                          6     traces that named this subject themselves

by day
  Mon 2026-05-04  attention 0:06  background -  2 events
  Tue 2026-05-05  attention 0:06  background -  2 events
  Wed 2026-05-06  attention 0:06  background -  2 events
```

The name is matched without regard to case, and a name that is not a subject
says so and lists the ones that are — zero hours and a misspelling look
identical otherwise. Where subjects come from is [Subjects](#subjects) below; with no rules there
are none.

`--unmatched` is the other side of the dictionary: the directories and browser
keys in range that no rule mentions, busiest first, written the way they go
into the config. It reads the whole database too.

```
spoor report --unmatched
```

```
no rule mentions these, 2026-05-04 to 2026-05-06

working directories — a rule on the directory above them makes them one project
DIRECTORY            TIME  EVENTS  CALLED NOW
/home/u/src/scratch  1:06  12      scratch

browser keys — paste one into keys:, or into never: if it serves every project at once
KEY                           TIME  EVENTS  CALLED NOW
status.example.net/incidents  0:09  3       payments-api
wiki.example.com/spaces       0:09  3       payments-api
tracker.example.com/browse    0:06  3       payments-api

A name in the last column is a guess, not a rule: for a directory it is the last element of the path, which splits one project across the directories inside it and merges unrelated ones that end in the same word; for a browser key it is whatever the block around it was called.
```

Anything already on `never` — a key or a directory — does not appear: a
decision was made about it. That is what makes both lists shrink as the
dictionary is written, and what makes this the answer to "which line should I
add next".

`TIME` is how much of the day happened around each one — the stretch that
begins with each of its traces. It answers "is this worth a line", and it is
**not** a figure from the report: it is charged to whatever was on screen at
the time rather than to the project the minutes counted for, so these numbers
do not add up to anything printed elsewhere.

Both examples are the same invented three days, run through a dictionary that
names one project and reads issue keys out of page titles. The shape is what
`spoor` prints; the hosts, the directory and the ticket are made up.

### What the numbers mean

**Blocks.** Events no further apart than `--gap` — ten minutes by default —
are one block of work. A pause of exactly the threshold is still one
block; one second more is two. The pauses *between* blocks are not counted at
all: `spoor` does not decide whether your lunch was work, so it does not
quietly bill it.

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
`claude-code`, `browser` or `calendar`. Days are local days: timestamps are
stored in UTC, and the local day boundaries you give it are converted to UTC
when querying.

A meeting is the exception to all of this, and the only one. Every other source
records a moment, and the day is made of the stretches between those moments; a
meeting carries its own length, because nothing is written to disk while you sit
in one. So an hour in a call is an hour of **attention** rather than a point
casting a window, it names the seconds it covers the way a typed prompt does,
and a block holding a meeting runs at least to the end of it. A meeting that
would run past midnight is cut there: a day holds at most a day. Anything
longer than a day is not imported at all — see what it skips, under
[Network](#network).

**`confirm`** is the day you agree with. It and `assign` are the two commands
that write to your config file.

It opens on a queue of questions, busiest first. A question is about a *trace* —
a working directory, a browser key — rather than about a block: one uncovered
key seen in eight places is one question, because the answer closes all eight.
Where the segments of a host separate nothing, the question is about the host,
so fifteen segments are one question rather than fifteen. The screen says how
much of the day turns on the trace, where in the day it turned up, what was on
either side of it, **what the pages were called**, and **what it counts for
now** — that last one because a key
already inherited correctly and a key leaking into the busiest project nearby
look identical without it.

**Naming a subject names the project too.** A subject is only looked at once a
project has matched, so a rule filed under the subject and nowhere else names
neither — and the same question comes back the next day. Answering with both
writes the trace under both, unless the project already names it.

The answer is a project and, optionally, a subject inside it; both can be
created on the spot. Two more cost a single keystroke each: **this names
nothing**, for a search engine or a wiki root, and **no rule can name this**,
for a trace that does name a subject and never the same one twice — the second
is what puts a trace on the `ambiguous` list, and every later visit to it is
then a short question of its own — which subject this one was — with no further
question about the trace itself.

A key question can also be answered **name it by the page title**, which is
what a repository host needs: the key there is the host and the account,
because the repository name is a path segment `spoor` deliberately does not
store. It is in the title, which the screen shows under `SEEN AS`, and that is
what this answer writes the rule from.

**Every edit is then asked what it is**, in one keystroke:

| | what it does |
|---|---|
| this block only | no rule; nothing else moves |
| a rule | rules run when a report is built, so every day not yet confirmed is renamed |
| a rule from the day you are looking at | the same, forward only — everything before that day keeps the name it had |

The third exists because rules change in time. The same directory was one
project six months ago and is another now, and an undated rule says it always
was this one. The date written is **the day being confirmed**, not the day you
are sitting at: confirming Monday on Wednesday dates the rule to Monday, so
Monday and Tuesday move with it and the week before does not.

Which of the three an edit is cannot be deduced from the edit, so it is asked
every time rather than configured once.

**A shortcut is a place on the keyboard, not a letter.** `t` and `е` are the
same key and do the same thing, and so is every other pair on a ЙЦУКЕН layout
over QWERTY — switching layouts to press one letter, and switching back, is a
tax on the one loop that has to fit in two minutes. Typed text is untouched: a
project called `модель ЗП` is typed the way it is spelled.

Nothing is written before it is shown. The line that will go into the config is
printed first, and afterwards the day is recomputed and the difference printed —
`payments-api +1:12, no project −1:12`. A rule that moved no time says so, because
that is either a rule something more specific already refuses or a rule on a
trace nobody touches, and both look exactly like a rule that worked.

**The pauses between blocks are part of closing the day.** After the questions,
and after the short one-visit questions an `ambiguous` trace raises, `confirm`
walks them one at a time — `w` jumps there
directly, and a day whose pauses are all answered does not stop on them again.

A pause takes the same four answers a question does — `enter` attach it to what
is selected, `tab` move between the project and the subject, `N` make a new
one, `n` it was not work — and so does a block you opened with `b`. The other
three screens have less to say and show fewer keys: the day, the background
line, and the one visit an `ambiguous` trace raises.

**A pause is never asked to become a rule.** It has no trace at all; that is
what makes it a pause, and there is nothing a rule could be written on. So the
"what is this edit" question every other correction is asked does not appear
here, and nothing an answer here does reaches your config file.

What a pause *was* is the one question nothing on this machine can answer — a
cigarette and a meeting leave the same absence of traces — so the tool shows
the pause, shows what was on either side of it, and lets you say.

What the answer buys is **a line of its own**: `pauses you called work`, in the
report, in `--json`, in an export and against the project it was attached to.
On the schedule the pause is marked `— you called this work —` where it would
otherwise say `— nothing —`.
It is never added to `attention`, to `active` or to the coverage figure, and
nothing about the measured day moves — for the same reason the background line
works that way. One number made of a measurement and somebody's recollection is
a number nobody can check.

**A confirmed day still opens for them.** `spoor confirm --day=D` on a frozen
day says so and lands on the pauses, which are the only thing left that can be
answered — nothing they record is a measurement, so locking them behind
`--reconfirm` would mean throwing a snapshot away to answer a question that
does not touch it.

Without a terminal: `confirm --windows` lists them, `--windows --json` as a
document, and `confirm --window=14:05-14:22 --worked=true --window-project=NAME
[--window-subject=NAME]` answers one.

Bare `confirm` refuses to start without a terminal and names the two flags that
work anywhere. `--questions` prints the queue instead of opening the terminal,
`--json` as a document. `--yes` confirms the day as it stands. `--list` prints
the confirmed days and which of them the rules have moved under since.

A confirmed day carries the answer and not the evidence: the projects, the
subjects, the four numbers, and the day cut into stretches with what each one
rests on. The `ON WHAT GROUNDS` column is empty for it, and `--json` carries no blocks,
sources or browser keys — those live in the events table, where they always
did, and `report --recompute` shows them. The event counts are kept.

Confirming freezes the **result**, not the events. `report --day` then reads
the snapshot rather than computing it, so a day you have already acted on stops
moving when a rule changes. Importing into a confirmed day is not forbidden —
the events land, the day does not move. `spoor report --day=D --recompute` shows what the rules
would say now without writing anything, and `spoor confirm --day=D --reconfirm`
reopens the day.

**`assign`** is one answer without the terminal, through the same code:

```
spoor assign --day=2026-09-08 --trace key:example.com/issues --project Widgets
spoor assign --day=2026-09-08 --trace path:~/src/thing --never
spoor assign --day=2026-09-08 --trace path:~/src/thing --project Widgets --since
spoor assign --day=2026-09-08 --from 14:05 --to 14:22 --project Widgets --one-off
```

`--trace` takes `path:`, `key:`, `branch:` and `title:` — the four kinds of
rule. The first two are what the queue asks about, because they are the two
that can be listed; the other two are there so that a rule the terminal offers
can also be written by hand.

`assign` refuses a day that has been confirmed, whichever form it is used in,
and says which command reopens it: a frozen day is frozen against its own
author too. `confirm` on a frozen day opens the pauses instead, as above.

`--dry-run` prints the line and writes nothing. An answer about a stretch of one
day cannot become a rule and has to say so with `--one-off`; those are counted,
and the count is printed when the day is confirmed, because hand marking that
nobody can see is hand marking nobody revisits.

**`export`** writes a confirmed day out. One format today, `markdown`.
`--timeline` adds the schedule, and it is the stored partition rather than the
report's: one row per stretch with one answer to every question, which on a
working day is tens of rows where `report --timeline` prints a dozen.
`--unconfirmed` exports a day that has not been confirmed and marks it as
provisional on the page. **No trace is printed in an export** — no host, no
directory, no page title. This is the first thing that leaves the tool, and the
title of a search results page is the search query.

Two exceptions, both deliberate, both yours to turn off:

A project no rule names keeps the guess its working directory gave it, and that
guess is the last element of a path. It is printed, because it is the name that
project has in every other view and an export that renamed it would be a
different document about the same day. `fallback: none` turns the guess off,
and naming the project removes it; both are one line, and both are the decision
you already made for the report.

And a subject with no name takes it from the first capture group of its own
expression — `titles: '\b([A-Z]+-\d+)\b'` makes the ticket number the
subject. That is the point of the shape and it is why it is offered, but what
lands in the column is a piece of text off a page title, so an expression
loose enough to capture half a title will put half a title in the export.
Nothing here guesses that for you: the expression is one you wrote, and what it
captures on a page you did not have in mind is the thing to check. Naming the
subject removes it — a named subject stores the name you gave it and never the
text it matched.

**`version`** prints `dev` unless the binary was built with `just build`, which
stamps the version from git.

## Where things live

Everything follows the XDG base directory spec:

| What | Where | Override |
|---|---|---|
| Database | `~/.local/share/spoor/spoor.db` | `SPOOR_DB`, `XDG_DATA_HOME` |
| Config, optional; `confirm` and `assign` add rules to it | `~/.config/spoor/config.yaml` | `--config`, `SPOOR_CONFIG`, `XDG_CONFIG_HOME` |
| Calendar URLs, written by `add-calendar` | `~/.config/spoor/calendars/<id>` | `url_file:`, `XDG_CONFIG_HOME` |
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

There is no config file until you make one. Two commands write to it —
`spoor confirm` and `spoor assign` add rules to its `attribution` section, and
create the file if it is not there; every other command only reads it. It holds
four sections today: the browser source, the calendar source, the thresholds
the report is built on, and the dictionary that says which project something
belongs to.

What they write, they write carefully. The file is located with a YAML parser
and edited as text, so comments, order and the shape of every list somebody
chose come back exactly as they were, and the only lines that change are the
ones that were added. The one exception is a rule written as a single value
rather than a list — `paths: ~/src/thing` — which has to become a list to hold
a second entry; its value is carried across unchanged, and the
formatting of that one line is not: the comment on it, if there was one, ends
up above the list rather than beside the value. A copy is kept beside the file
before the first write of a session, the write is atomic, the permissions are
the ones that were there, and a file that changed on disk since `spoor` read it
is refused rather than overwritten.

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

calendar:
  # The network switch. False, or absent, and no calendar is fetched — a
  # "file:" source below still works, because it goes nowhere.
  # See Network, above the install instructions.
  enabled: false
  sources:
    - id: work
      # Where the URL is. Left out, it is ~/.config/spoor/calendars/<id>,
      # which is what `spoor add-calendar` writes.
      # The URL itself never goes in this file: this is the file you paste
      # into an issue, and it reads your whole calendar.
      url_file: ~/.config/spoor/calendars/work
      # Your own addresses, so that a meeting you declined can be told from
      # one somebody else declined. The feed says which attendee said no; it
      # does not say which attendee is you. Left out, nothing is skipped on
      # that ground. These are read and never stored.
      me: you@example.com
    - id: team
      # A downloaded .ics instead. Needs no network and no switch above, and
      # goes stale silently — it is only as fresh as your last download.
      file: ~/Downloads/team.ics

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

attribution:
  # Traces that must name no project: a host that serves all of them, a
  # directory you have decided says nothing.
  never:
    keys:  [www.example.com]
    paths: [~/scratch]
  # Traces that do name a subject and never the same one twice: an operations
  # console is checking what you shipped one hour and a fire the next. No rule
  # separates those, so no rule tries. min_split is how long a visit has to be
  # before it is worth asking about at all; 0 means zero, not "the default".
  ambiguous:
    min_split: 5m
    keys: [ops.example.com]
  # One line for every ticket there will ever be: no name, so whatever the
  # capture group matches becomes the subject.
  subjects:
    - titles: '\b([A-Z]+-\d+)\b'
  projects:
    - name: payments-api
      work: true
      # host[:port][/first-segment], as the report prints it.
      keys: git.example.com
      # A regular expression over the page title.
      titles: 'payments-api'
      # A directory and everything under it. ~/src/shared was this project
      # until the tenth; see billing below.
      paths:
        - ~/src/payments-api
        - ~/src/shared
    - name: billing
      # ~/src/shared moved here on the tenth. See below.
      paths:
        - {value: ~/src/shared, since: 2026-09-10}
```

**A dated rule beats an undated one of the same specificity**, and between two
dated ones the later start wins. So "this directory is X, and Y from the tenth"
is two entries and needs no end date on the first: before the tenth the dated
rule does not apply, from the tenth it wins the tie.

`{value: ..., since: ...}` stands in for a bare value in any of the four lists
a rule can be written on — `paths`, `keys`, `branches`, `titles` — and in the
two lists of `never` and `ambiguous`, which take `keys` and `paths` only.

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

### Naming projects

Without this section a project is the last element of the working directory a
Claude Code session ran in, and a browser visit has no project at all. That
guess is wrong in both directions at once: it splits one project across the
directories inside it — a checkout and its `docs` are two projects, and a
single chat window changes its own directory the moment a shell command does —
and it merges unrelated checkouts that both end in `src`.

The dictionary replaces it. Each entry is a name and the rules that give an
event that name:

| Key | Matches | Against |
|---|---|---|
| `paths` | a directory and everything under it; `~` for home, a trailing `*` for a plain prefix | the working directory of a Claude Code event |
| `keys` | `host[:port][/first-segment]` | a browser visit |
| `branches` | a regular expression | the git branch |
| `titles` | a regular expression | the page title, or a meeting's summary |

Any one of them matching is enough. Each takes a list, and a list of one may
be written as a plain value — `paths: ~/src/thing`.

**A directory rule covers what is under it**, so one line takes a checkout,
its subdirectories and the worktrees inside it. `/src/thing` does not cover
`/src/thing-other`: the comparison ends at a separator, which is the whole
point. A trailing `*` makes it a plain prefix instead — `paths: /tmp/thing-*`
— which is what the temporary worktrees an agent creates need, since each has
a random suffix and none of them is under a shared directory.

**A key is written the way `report --unmatched` prints it** — that command's
`KEY` column is a key and nothing else, so a line of it can be pasted in
unchanged. The same value appears inside the `ON WHAT GROUNDS` column of the
day report, with a visit count after it; the count is not part of the key.

A bare host covers its subdomains and every first segment of it. Written out,
a port and a segment must both be the ones written: `localhost:3000` and
`localhost:5173` are two dev servers and stay two, and `example.com/issues`
matches only that first segment. An address is matched exactly and has no
subdomains, so `[::1]:3000` and `192.0.2.10` mean themselves and nothing
under them.

**Quote a key that starts with a bracket**: `- '[::1]:3000'`. Unquoted, `[`
starts a list in YAML and the whole run stops with a parser error — the same
trap as the `ignore` list further down, and the same fix.

**The more specific rule wins, whatever order they are written in.** A longer
path beats a shorter one and a longer key beats a shorter one, so
`app.example.com/salary` can belong to one project while the rest of
`app.example.com` belongs to another. A literal beats a regular expression:
where something happened is better evidence than what a piece of text looked
like. Where two rules are equally specific, the one with a later `since` wins,
and an undated rule counts as the earliest there is. Between two expressions
that are equal on all of those there is nothing left to compare, so the order
they are written in decides, always the same way.

**`ambiguous`** is the second decision a trace can get, and it is not
`never`. `never` says the trace names no project. This says the trace does name
a subject and never the same one twice: the host of an operations console is
checking what you shipped one hour and a fire the next, and no rule separates
those.

So what is decided here is "no rule can name this", once and for all — that
question is never asked again. Each later *visit* is a short question of its
own, with the subject you were on either side of it offered first and the
project's other subjects under it, and the answer lives in that day rather than
in this file. `min_split` is how long a visit has to be before it is worth
asking about at all; below it the time stays with whatever you were on around
it. It defaults to five minutes — one person's measurement of their own days —
and `0` there means zero rather than "the default".

**`fallback`** says what happens to a Claude Code event no rule names:
`cwd-basename`, the default, keeps the guess so that adding a first rule
cannot take a name away from an event that had one; `none` leaves it unnamed,
to be named by the block around it or not at all.

**`work: true`** marks a project as work rather than personal. Leaving it out
says nothing, which is not the same as personal — on the data this was
measured against, personal projects carried more than twice the hours of work
ones, so a tool that assumed either way would be wrong about most of the day.
It appears as `work` in `--json` and is not otherwise summed anywhere yet.

**`never`** lists traces that must not name a project — browser keys under
`keys`, directories under `paths`:

```yaml
attribution:
  never:
    keys:  [www.example.com, wiki.example.com/search]
    paths: [~/scratch, ~/tmp-notes]
```

A bare list is read as `keys`, which is what this section was before `paths`
existed.

A search engine, a wiki root or an issue tracker's home page serves every
project at once — one such key was a sixth of all browsing on the data this was
written against — and for those the right answer is no project rather than the
wrong project. What the time was about is decided by the block around it, which
is where the evidence actually is.

**A dev server can go either way, and only your own data says which.** The port
is stored precisely because it tells one server from another — without it every
`localhost` is one key. Whether that server is one project is a separate
question: a port you always run the same thing on is a `keys` entry for that
project, and a port you reuse for whatever you are working on today belongs
under `never`, because the block around it knows and the port does not.

**A directory needs this more than a host does.** A host with no rule is simply
unnamed; a directory with no rule is named by the last element of its path,
which is a guess nobody wrote down. The only other way to refuse that guess is
`fallback: none`, which turns it off everywhere at once — so a directory you
have never seen before stops appearing as a row and quietly joins its
neighbours instead. A path here is how you decide about one directory and keep
the guess working for the rest.

Three things follow, and each is there for a reason:

- **it takes the trace away, not the event.** A rule reading the page **title**
  or the **branch** still applies. That is what makes an issue tracker work
  here without an API: the host says only "the tracker", while the title says
  which board or which repository;
- **a path here beats the guess**, or the last element of the path would name
  what the entry just refused;
- **a path here names one directory, not the tree under it** — unlike a path
  under a project, which names the tree. Silencing is a statement about the
  directory you looked at, and `report --unmatched` prints your home directory
  as a candidate: a subtree would take one paste to switch discovery off for
  the whole machine.

  For the tree, add a second entry: `[~/scratch, ~/scratch/*]`. A trailing `*`
  is a plain string prefix, exactly as it is under a project — `~/scratch*` on
  its own would take `~/scratchpad` and `~/scratch-notes` with it, and say
  nothing about having done so;
- **it loses to a more specific rule of its own kind.** `never` on `/home/u`
  does not take away a rule on the one checkout inside it, and a bare host
  under `never` does not take away `keys: docs.that-host`. Conversely a
  narrower `never` carves a segment out of a broader rule — `never` on
  `wiki.example.com/search` under a project that owns `wiki.example.com`.

Everyday use, beyond refusing: a key or a path nobody has written a rule for is
unnamed anyway, so putting it here says so **on purpose**, which is what takes
it out of `report --unmatched`.

A rule that can never match anything — a key written as a URL, a path that is
not absolute, a project with no name, an expression left empty, or a rule a
`never` entry covers just as specifically — is a warning on stderr, not a
failed run. The last of those is the one worth reading: the others are typos in
a single line, while that one says two lines that are each correct have
cancelled each other out. One unusable line is not a reason to refuse to
report, and a line that silently does nothing is the failure this is here to
prevent. It goes to stderr rather than into the output because `--json` has to
stay a document a program can read.

### Subjects

A subject is the second level and the last: something inside a project that
spans weeks — episode 14, level 3, one feature, one ticket. There is no third
level, on purpose. A tree of any depth would need every report to say which
depth it was answering about, and the question people ask is "how long did
this one thing take".

A subject is written like a project rule, with a name, inside the project it
belongs to:

```yaml
attribution:
  projects:
    - name: payments-api
      paths: ~/src/payments-api
      subjects:
        - name: refunds
          branches: '^refunds/'
```

With **no name**, the first capture group of whichever expression matched
becomes the subject. That is the line worth having:

```yaml
attribution:
  subjects:
    - titles: '\b([A-Z]+-\d+)\b'
```

Written at the top of `attribution` it applies inside every project, and it
gives every ticket a subject of its own without listing any of them. This is
the whole of the issue-tracker support: the key is in a path segment `spoor`
deliberately never stores, and it is also in the page title, which it does.
The same expression finds it in a merge request title and in a branch name.

Two things follow from a capture group being what names a subject. Whatever it
matches is printed, so an expression that captured half a page title would put
half a page title on screen — `([A-Z]+-\d+)` captures a key and not the query
around it. And a subject with no name and no capture group cannot call itself
anything, so it is refused with a warning rather than quietly producing empty
ones.

**A subject never inherits.** A project can take its name from the block
around it; a subject cannot. A ticket number in a page title says that page
was about that ticket and says nothing about the hour that followed — so a
subject holds the stretch of the day around its own traces, and no more. The
subjects of a project therefore do not add up to the project: most of a
project's time belongs to no subject in particular.

#### A subject reaches as far as your touches do

Time inside a block belongs to the nearest **human touch** — a prompt you
typed, a page your browser recorded — not to the nearest event. A subject is
taken from that same touch, so a rule pointed somewhere your touches do not
reach collects traces and little or no time.

Two shapes, and they differ:

- **the events share a block with touches that are somewhere else** — you
  prompt from the parent directory while the agent works in the child. The
  stretch goes to the touch, so a subject rule on the child gets no time at
  all;
- **the events are a block of their own, with no touch in it** — a session
  resumed with its prompt in an earlier block. Then each event keeps its own
  name and the time is counted, as background: the agent working alone.

Measured on the data this was written against, all of these are real:

| A rule on | What it had | What it got |
|---|---|---|
| The hosts of dashboards and logs | your visits | hours, and a category that had been hiding inside others |
| A directory you prompt in | your prompts | hours |
| An issue key in a page title | a card open for a minute | 3.5% of the time that issue was worked on |
| A directory only the agent works in, prompted from its parent | 2983 events, no prompt of yours in the block | no time as a subject |
| Repositories inside one folder | no working directory of their own | nothing to tell apart |

None of that is fixable with more rules, and it is worth knowing before writing
any. What fixes it is leaving a trace:

- **use the same name everywhere** — the folder you open, the branch you cut,
  the ticket you file, the chat you start. One name, and one line of the
  dictionary finds all four;
- **give a piece of work its own directory** when you want its time counted
  separately, and prompt from inside it;
- **branch per ticket** if you want time per ticket. Every Claude Code event
  inside a git checkout carries its branch, so `branches: '\b([A-Z]+-\d+)\b'`
  starts working the day you do — and works backwards over everything already
  imported, because rules run when the report is built.

Where you did not, the time is still measured — it just belongs to the project
rather than to anything below it. Naming that by hand is what the terminal
interface is for, and it is a much smaller job than remembering when it
happened.

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

Expect two lines per source. A calendar adds up to two more, in this order:
`restated:` when it has moved, renamed or dropped a meeting since the last run,
and `skipped:` when it refused something it found. Neither is a fault —
a calendar does not append like the other two sources, it restates, so
correcting a meeting is the normal case. Then the total — five lines with both
Claude Code and a browser installed, in this shape:

```
claude-code: 120 files (120 read, 0 unchanged), 20000 lines
  events: 14000 found, 14000 new, 6000 session-state lines skipped
browser: 1 profiles (1 read, 0 unchanged), 9000 visits
  events: 8800 found, 8800 new, 0 ignored, 200 not http(s)
database: 22800 events total
```

The numbers above are made up; yours will be your own. A month of session
logs and 90 days of browsing take a second or two together. Other extra lines
appear only when a source dropped something — an unreadable line, or a meeting
the calendar skipped — and **a source that found nothing
to read is not mentioned at all** — with only one of those two installed you get
three lines, not five, and that is not a fault.

**3. The second import, straight after — this is the important one.**

```
spoor ingest
```

Expect `(0 read, N unchanged)`, `0 new` — on the two local sources. A calendar
is different and always will be: it is fetched and re-read every run, so it
reports `(1 read)` every time and has no `unchanged` count at all. What to look
for there is `0 new` and **no** `restated:` line; that line is printed only
when something actually moved. Otherwise, expect `(0 read, N unchanged)`,
`0 new`, the same total as before, and no
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

That is the whole record of one event. Seven other tables exist; this prints every
column of every one of them, with the comments that sit beside them:

```
python3 -c "import sqlite3,os;d=os.path.expanduser('~/.local/share/spoor/spoor.db');\
print('\n\n'.join(r[0] for r in sqlite3.connect('file:'+d+'?mode=ro',uri=True).execute(\
\"SELECT sql FROM sqlite_master WHERE type='table' ORDER BY name\")))"
```

```
sqlite3 ~/.local/share/spoor/spoor.db "SELECT sql FROM sqlite_master WHERE type='table' ORDER BY name;"
```

What they are, in one line each:

- `source_files` — import bookkeeping: paths, sizes, read offsets and how far
  each browser profile has been read. No event content.
- `assignment` and `confirmed_one_off` — what you typed when you corrected a
  stretch of a day by hand: a time, a project name and a subject name. The
  `reason` column beside them is spoor's own note of which answer put the row
  there ("answered for this block only"), one of a handful of fixed sentences,
  never anything you wrote.
- `window_answer` — what you said about a pause between blocks: whether it was
  work, what it was, and what you were on either side of it.
- `confirmed_day`, `confirmed_stretch` and `confirmed_row` — the days you
  confirmed: the settings they were computed with, the day cut into stretches
  with a project and a subject on each, and the table you agreed with.

Every name in all of them is one you wrote in the config or typed into the
terminal, with three exceptions. `source_files` holds the paths of the files
that were read, which spoor found for itself. A project no rule names keeps
`basename(cwd)`, so a directory name can appear as a project name. And a
subject with no name of its own takes it from the first capture group of its
expression, so a piece of a page title can land in a subject column — that one
is the shape [Subjects](#subjects) recommends, and it stores what your
expression captured rather than what you typed. `fallback: none` removes the
second; naming the subject removes the third. Both are also the two exceptions
the export carries, for the same reason.

To read one of those tables in full, with nothing trimmed — the name at the end
is the table, and `confirmed_row` is the one to start with, since it is the day
you filed:

```
python3 -c "import sqlite3,os,sys;d=os.path.expanduser('~/.local/share/spoor/spoor.db');\
c=sqlite3.connect('file:'+d+'?mode=ro',uri=True);t=sys.argv[1];\
print(*[dict(zip([x[0] for x in c.execute('SELECT * FROM '+t).description],r)) for r in \
c.execute('SELECT * FROM '+t)],sep='\n')" confirmed_row
```

```
sqlite3 -header ~/.local/share/spoor/spoor.db "SELECT * FROM confirmed_row;"
```

The shorter queries below only trim the view; they hide nothing.

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

Meeting summaries are in the same column and the same list, with an empty
`host`: a meeting has no domain, so the ignore list cannot reach it. A meeting
called "1:1 with Sam" puts Sam's name here, and the controls for that are the
ones under [Network](#network) — leave the calendar out, or point it at a
calendar you are happy to have on disk.

**7. An event outlives the file it came from.**

This check runs on a copy of one session, in a temporary directory: your real
logs and your real database are never touched.

```
SPOOR_TMP=$(mktemp -d) && mkdir -p "$SPOOR_TMP/p" && (
  set -e
  cp "$(find ~/.claude/projects -name '*.jsonl' | head -1)" "$SPOOR_TMP/p/"
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p" --no-browser --no-calendar --quiet
  rm "$SPOOR_TMP/p"/*.jsonl
  spoor ingest --db "$SPOOR_TMP/db" --claude-dir "$SPOOR_TMP/p" --no-browser --no-calendar
); [ -n "$SPOOR_TMP" ] && rm -rf "$SPOOR_TMP"
```

The second import prints one line and nothing else — the source found no
files, so per step 2 it says nothing at all — and that line is the same
non-zero `database: N events total` as the first import. The source file is
gone; the events are not.

`--no-browser` and `--no-calendar` are not decoration. Without the first, this
throwaway database fills up with your real browsing history; without the
second, a check about local files goes to the network — which is exactly what
this README spends a section promising happens only when you ask for it.

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
nothing could name, and a project appearing twice under two names is the
working-directory guess doing what it does until you write a rule.

If a project shows `agent` time and no time of yours at all — neither
attention nor background — the report names it in a paragraph under the
summary. Nothing starts an agent but a person, so that combination means
either something ran unattended or — far more often — one chat window is being
counted under two names, because a shell command that changes directory
changes what the session calls itself. One `paths` rule on the directory above
both names is the fix, and the next step is how to find it.

**10. Write the first line of the dictionary.**

```
spoor report --unmatched
```

Every directory in the first list is a project the tool is naming by
guesswork. The one at the top costs the most time, so it is the line worth
writing first — and once it is written, it leaves the list. A directory that
turns out to mean nothing goes under `never: paths:` instead, which is just as
much a decision and takes it off the list too. The second list is browser keys,
with the same two answers: one that belongs to a single project goes under
`keys`, one that serves all of them under `never: keys:`.

Do the same a week later and the list will have changed. That is the loop —
the dictionary is not written once, and this is what says what it is still
missing. [Naming projects](#naming-projects) has the syntax.

## Uninstall

`spoor` leaves files in two directories and nowhere else — the copy it makes
of a browser history goes to a temporary directory and is deleted again. It
installs no service, no timer and no shell hook, so removing it is removing
files.

**Before you do: the database is the only copy.** Claude Code deletes its own
session logs after 30 days by default and Chrome keeps 90 days of history, so
everything imported from further back than that exists nowhere else on the
machine. There is no undo, and there is no way to export the database either — if the history
matters to you, `spoor.db` belongs in whatever backup you already run.

```
rm -rf ~/.local/share/spoor    # the database and its -wal / -shm files
rm -rf ~/.config/spoor         # config.yaml and config.yaml.bak, and calendars/ if you used add-calendar
rm -f  ~/.local/bin/spoor      # the binary, wherever you put it
```

If `SPOOR_DB` or `XDG_DATA_HOME` is set, the database is not there — print the
real path with the `echo` above before deleting anything. A custom `SPOOR_DB`
names the file rather than a directory, so remove its `-wal` and `-shm`
siblings alongside it.

`spoor add-calendar`, `spoor confirm` and `spoor assign` are what write under
`~/.config/spoor`. The first writes a calendar URL — a bearer credential that
reads your whole calendar and does not expire. The other two add rules to
`config.yaml` and keep one `config.yaml.bak` beside it. If you are giving the
machine away, reset the address in your calendar's own settings as well:
deleting the file here does not revoke it.

To wipe the collected data but keep using the tool, delete only the database.
The next `spoor ingest` rebuilds it from whatever the sources have not erased
yet — 30 days of session logs, 90 days of Chrome history — and no further
back.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
