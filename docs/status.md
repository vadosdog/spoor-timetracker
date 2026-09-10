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

*(Stage 1.5 added `host`, `port`, `path_head` and `title` to this table, and
`cursor` to `source_files`. They are described under that stage below.)*

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
  is the order in which they add hours. *(The browser arrived in stage 1.5.)*
- **No config file.** Everything is a flag or an XDG default. The first thing
  that genuinely needs configuring is the browser dictionary. *(It was, and
  the config file arrived with it in stage 1.5.)*
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
- **The database has no export and no backup, and it is the only copy.** Once
  a day has aged out of Claude Code's retention window, the events spoor
  imported from it exist nowhere else. Deleting the file — or losing the
  disk — loses that history for good, and the loss is silent: a later import
  brings back fewer days than the database used to hold, with nothing left to
  compare against. This is not hypothetical. The database was removed while
  testing the uninstall instructions, and re-importing brought back only what
  was still on disk; the days that had aged out between the original import
  and the reimport were gone. Either spoor grows an export command, or the
  README says plainly that this file belongs in whatever backup the user
  already runs. The README now says it; the export command is still missing.
- **Schema changes need an entry in `addedColumns`.** `CREATE TABLE IF NOT
  EXISTS` does nothing to a table that already exists, so a new column never
  reaches an older database on its own, and the failure is quiet: ingest keeps
  exiting 0 and importing nothing. There is a test for the upgrade path; keep
  it honest.

## Stage 1.5 — browsing history. Done 2026-09-07.

`spoor ingest` now reads two sources. The browser one imports Chrome's
`History` and Firefox's `places.sqlite`, and it is here rather than four
stages later because the survey said it carries about two fifths of the hours
and all of the time no other source can name.

### What is in place

- `internal/source/browser` — profile discovery, both database shapes, and
  the reduction of a URL to the metadata spoor keeps.
- `internal/config` — the first config file, `~/.config/spoor/config.yaml`.
  It does not have to exist. Today it holds the browser ignore list and extra
  history paths.
- `ingest` grew `--no-browser`, `--no-claude-code`, `--browser-history` and
  `--config`; `count --source browser` works with no change.
- The events table grew `host`, `port`, `path_head` and `title`;
  `source_files` grew `cursor`.

### What a visit becomes

```
source        'browser'
external_id   <flavour>/<profile>/<visit time, microseconds>/<visit id>
ts            the visit time, same format as every other source
duration_ms   always NULL. See below — the browser does report one
type          'visit'
subtype       how the page was reached: link, typed, reload, form_submit, …
entrypoint    'chrome' or 'firefox'
host          'gitlab.example.com'
port          '3000'; empty when the URL carried no port, or the scheme's own
              default — https://x:443/ and https://x/ are the same place, and
              storing them differently would split one host into two keys
path_head     the first path segment, capped at 64 characters
title         the page title, capped at 4096 characters, with anything that
              could misrepresent it replaced by a space
project       empty. Attribution is a later stage
```

Host, port and first segment are all three kept because none of them is
enough alone. One host serves several projects — a GitLab instance is one
host and a dozen projects — and on the machine this was measured on
`localhost` alone accounted for dozens of distinct ports, which is dozens of
dev servers that would otherwise be one.

**Nothing below the first path segment is stored, ever.** No query string, no
fragment, no second segment. That is where session tokens, password-reset
links and search terms live. There is a test that walks every column of every
row looking for one.

The segment is taken from the *decoded* path, so a `%2F` counts as the
separator it decodes to — `/%2F%2Fdeep%2FSECRET` yields `deep`, not the whole
escaped run with the tail still in it.

**What ends the segment is an allow-list, not a list of delimiters to watch
for**, and the list is derived from RFC 3986 §3.3 rather than assembled by
hand: letters, digits, marks, `-._~!$'()*+:@` and a space.

It is *derived from* the grammar, not equal to it, and deviates in three
places:

- **Four sub-delimiters are removed.** §3.3 names three of them itself —
  `;`, `=` and `,` — with the examples `name;v=1.1` and `name,1.1`. The comma
  is the one that looks harmless: it attaches a value with no `=` anywhere,
  so `/session,ABC123` would keep the value whole. The fourth, `&`, the
  section does not name — it belongs to the query grammar — but it is how
  values are joined everywhere else and removing it costs nothing.
- **A space is added.** It cannot appear in a URL unescaped, so it only
  arrives as `%20`, and without it a SharePoint or an intranet file server
  loses nearly every first segment it has.
- **ALPHA and DIGIT are read as Unicode categories, plus marks**, rather than
  as ASCII. That is the counterpart of `pct-encoded`, which otherwise has
  none here: the path arrives decoded, so a name in any script has already
  become letters. It is narrower than `pct-encoded`, not wider — an escape
  can decode to anything, and only the letters, digits and marks among those
  are kept.

Anything outside that ends the segment — `?`, `#`, `\`, a control character, a
private-use character, a delimiter invented after this was written.

Taking the set from the grammar is the point. The first version of it was
whatever punctuation turned up in one person's browsing, and a set assembled
that way grows by one every time somebody looks at different data: `@` for
Mastodon handles, then a space for SharePoint, then an apostrophe, then
brackets. Membership stops being a matter of whose data was to hand.

That is the second attempt. The first was a deny-list, and it had to be
extended three times: `?`, then `;` when a review found `/portal;jsessionid=…`
(what a servlet container writes when cookies are off), then `\` when the
next review found UNC paths. Every fix was right and every one left the same
question open. The allow-list closes it, and getting the set wrong now costs
a truncated project name rather than a leaked token. That is not free
either: two segments differing only past their first non-name character
become one value, and this column is what the reporting stage will group on.
The set therefore includes a space, which a SharePoint or an intranet file
server puts in nearly every first segment it has.

It was measured before it was written, on 730 distinct segments of real
browsing: 729 survive whole. The one it shortens ends in U+E000, a
private-use character — invisible, and outside every category the deny-list
knew about. It had already reached the database.

A path is percent-encoded **bytes**, not text, so what is stored is repaired
first. A site older than UTF-8 encodes its path in the page's own charset —
`/caf%E9/` is Latin-1 — and a profile directory name is bytes too. SQLite
stores such bytes happily and then refuses to hand the column back, so one row
of them turns `SELECT *` from a wrong answer into no answer at all.

**Every column carrying bytes from the browser goes through the same
repair** — the host, the first path segment, the title, and the profile name
inside the dedup key. Not a list of the ones that came to mind: this was got
wrong twice, each time in a column the previous fix did not reach, and the
test now checks every string field on the event, including the six this
source never sets: asserting they stay empty costs a map entry and catches
the day a browser value starts reaching one of them.

The other text this source writes is not repaired because it cannot need it:
`type`, `subtype` and `entrypoint` are constants of this package, and `port`
is digits or nothing, because `url.Parse` refuses a port that is not.

The repair costs fidelity and is worth it: a run of invalid bytes collapses
to one replacement character, so two different paths can end up looking the
same in the database.

It also closes an asymmetry that was not obvious. The ignore list refuses an
entry containing a character that could misrepresent it, so a host allowed to
keep one would have been a host nobody could ever exclude. The producer and
the validator have to agree about what a host may contain, and now they share
the call that decides.

### The database belongs to a running browser

Both browsers hold their history open. spoor never opens one *as a database*:
it reads the file through byte for byte — along with whatever `-journal`,
`-wal` or `-shm` sits beside it — into a private temporary directory, works
on the copy, and deletes it. So no lock is taken and nothing is written back.
A test asserts the original's size and mtime are unchanged after an import
that ran while a writer held an open transaction.

The copy is not atomic, so a visit made during it is picked up by the next
run rather than this one.

### Incremental, but by time rather than by offset

A JSONL log only grows, so the Claude Code source remembers a byte offset. A
history database is rewritten in place and also forgets — Chrome keeps 90
days — so there is no offset to keep. Each profile remembers the timestamp of
the newest visit it imported (`source_files.cursor`) and asks for everything
from a week before it onwards.

**A week, not the exact watermark**, because a history does not only grow
forwards. Browser sync writes another device's visits carrying their original
timestamps; a clock corrected backwards, a resume from suspend and a restored
profile all insert rows behind the newest one. With no overlap those rows
would be read never and nothing would say so. Re-reading a week is a few
thousand rows thrown away on the dedup key, which costs milliseconds.
Anything backdated by more than a week is still missed — that is the residue
of choosing to be incremental at all, and it is why `ingest` sometimes
reports visits read and no new events.

**And a visit dated in the future never moves the mark.** It is imported like
any other, because it did happen, but a clock that was wrong once — a
restored snapshot, a dual-boot RTC read as local time, a device syncing while
running fast — would otherwise park the watermark years ahead and stop the
profile importing anything at all until that date arrived. Silently, and not
undone by deleting the offending row. The overlap is the guard against skew
backwards; this is the guard forwards, and without both the two directions
are not survivable in the same way.

A profile whose file and sidecars have not changed at all is not even copied.

### Acceptance

Measured against a real profile over a two-week window that had closed and
could no longer grow. The window, the counts and the method live outside this
repository with the rest of the measurements — absolute numbers with dates on
them reconstruct somebody's working schedule, which is not what a public
repository is for. What can be said here is the shape of the result:

| Check | Result |
|---|---|
| Two `ingest` runs in a row | second run: 0 new events, 0 profiles read, hundredths of a second |
| Claude Code hours, day by day | identical to the survey on every day of the window |
| Claude Code + browser hours | 0.9% below what the survey predicts for those two sources |
| Days with no browsing at all | none |
| Full URLs or query strings in the database | none |

Under a tenth of the tolerance the stage allowed. The remaining 0.9% is
accounted for, not absorbed: it is the non-web visits described below.

### What is deliberately not done

- **The reported duration is not stored.** Chrome records
  `visit_duration` — the time until that tab navigated somewhere else. Summed
  over two weeks of real data it came to twenty-five times the length of the
  period, because a tab left open overnight reports a visit lasting all
  night. It is a real number answering a different question, and a column
  called `duration_ms` would invite the reporting stage to add it up. Firefox
  records no duration at all, which is the honest answer.
- **Only http and https.** `file://` and `chrome-extension://` visits are
  counted in the report and dropped: every field this source stores describes
  a web address, and the first path segment of a `file://` URL is a directory
  on this machine, which is the file source's business. About 2% of visits
  inside the measured window, and the whole of the 0.9% gap above. Over a
  whole profile the share is smaller — the two numbers have different
  denominators and should not be added to anything.
- **No project.** Every browser event has an empty `project`. A
  domain→project dictionary is the attribution stage; guessing here would
  hard-code somebody's dictionary into a parser.
- **Chrome and Firefox only, by name, and only Chrome for real.** A file
  called `History` is read as Chrome and one called `places.sqlite` as
  Firefox, so anything else — Brave, Chromium, Edge, Vivaldi, a Firefox
  fork — can be pointed at without code knowing that browser exists. Whether
  it reads is untested: Chrome is the only browser this has been run against
  on a real profile, and the Firefox reader has never seen one at all. The
  README says so rather than implying it works.

### Loose ends for whoever comes next

- **`title` is the revealing column now, more than `raw_text` ever was.** A
  search result page has the query in its title, and on real data about a
  quarter of all browser events are on one. This is metadata by the rules of
  the project and it was asked for by name, but it is the field to look at
  before showing the database to anyone, and the reason the ignore list
  exists. It is capped at 4096 characters, which is what Chrome itself
  stores, so on real data nothing is lost — but a `--browser-history` file
  written by something else could be truncated. Control characters, the line
  separators and the format characters become spaces: a newline would turn
  one row of `sqlite3 -line` into two, a NUL makes SQLite's own `length()`
  under-report, and U+202E reverses everything after it, so a title that
  reads one way on screen would be another thing entirely in the database.
  That filter is a blunt one and it has a cost: it covers the whole format
  category, so a zero-width joiner goes too, and a title with a joined emoji
  sequence in it comes back visibly changed. Worth it for U+202E, but it does
  mean a stored title is not always character-for-character what the page
  said.
- **The first path segment is not inherently safe either.** The rule keeping
  the second segment out — `/reset-password/<token>` — does not generalise
  upwards. `meet.google.com/<code>`, `forms.gle/<token>`, a link shortener's
  slug: for those the whole secret *is* segment one. The ignore list is the
  only control, and it works per host. Do not read "first segment only" as
  "therefore harmless".
- **Renaming or moving a profile directory duplicates its visits.** The
  profile's name is part of the dedup key and its path is the bookkeeping
  key, so `~/.mozilla/firefox/x.default` and the Flatpak copy of the same
  profile are two identities, and every visit still inside the browser's
  retention window is imported twice. Nothing warns. The alternative —
  leaving the profile out of the key — trades visible duplication for silent
  loss when two profiles collide, which is worse, so this is the deliberate
  half of the trade rather than an oversight. Triggers: renaming a Firefox
  profile, moving between a distro package and Flatpak or Snap, restoring a
  profile backup somewhere else.
- **A parsing rule applies only to what has not been imported yet.** Rows
  are never re-parsed — the watermark sees to that, and re-import is
  deduplicated — so the same URL visited before and after a rule changed
  yields two different values. The author's own database still holds one
  `path_head` ending in the U+E000 that motivated the allow-list. Harmless
  here, since these columns are new in an unreleased stage, but the first
  person to group by `path_head` over a long window will meet it, and so
  will anyone who changes a rule later.
- **A warning about an ignore entry prints that entry**, and the ignore list
  is by definition the domains its owner least wants seen. There is no way
  round it — a warning that does not name the entry cannot be acted on — but
  it is worth knowing before pasting the output of `ingest` into a bug
  report, which the README invites people to do. The exposure is bounded to
  entries that are malformed, which are rare by construction.
- **`source_files.path` is the one text value that is not repaired**, and it
  cannot be: it is the lookup key for "how far have I read this file", so it
  has to be the path exactly as the filesystem gives it. A profile directory
  whose name holds a byte that is not UTF-8 therefore makes `SELECT * FROM
  source_files` fail the way `events` used to — the events themselves are
  fine, and the bookkeeping table is unreadable until that profile is gone.
  Nobody has hit this; it needs a deliberately odd directory name.
- **A copy of the history sits in `$TMPDIR` while a profile is being read.**
  It holds everything spoor promises never to store — full URLs, query
  strings, tokens — at mode 0600 in a 0700 directory, and it is removed on
  every return path. It is not removed if the process is killed mid-import:
  there is no signal handler. `Ctrl-C` during an ingest therefore leaves one
  behind, and `$TMPDIR` is where to look.
- **The ignore list only applies to what has not been imported yet.** It runs
  before the insert, which is what makes it a privacy control rather than a
  display filter — but a domain added to it today leaves yesterday's rows
  where they are. Removing those is a `DELETE` by hand. Whether `ingest`
  should apply the list backwards is an open question: it would make the list
  authoritative, and it would also mean a typo in a config file deletes
  history that exists nowhere else.
- **macOS and Windows profile locations are unverified.** They come from each
  browser's documentation, and nothing has ever run there. A wrong constant
  shows up as "0 profiles", not as an error.
- **The Firefox reader has never seen a real profile.** There is no Firefox
  on the machine spoor was written on. It is written against the published
  schema and covered by tests on synthetic databases, which is not the same
  as having been used. The first person to run it on a real profile should
  check the visit count against the browser's own history page.
- **Firefox's `moz_historyvisits` has no equivalent of Chrome's
  `AUTOINCREMENT`**, so its ids can in principle be reused after deletions.
  The dedup key carries the visit time as well as the id, which covers it,
  but the belt is doing work the braces were not.
- **A torn copy could in principle cost visits.** The watermark advances to
  the newest visit actually read, so a copy that lost recent pages is
  harmless — the next run picks them up. A copy that lost a page in the
  middle while keeping a later one is not, and nothing detects that. It has
  not been seen; it is written down because it would be silent.
- **Two profiles of the same browser both get imported**, and the survey's
  assumption that only one is live no longer holds — a long-dead second
  profile contributes nothing and is still read every time.
- **`--browser-history` replaces the config's `history` list too**, not just
  the automatic search. That is the intended meaning of a flag overriding for
  one run, but it is not what "the additive version" would lead you to guess.
  There is also no way to read *only* what the config lists: the choice is
  discovery plus the list, or the flag, or `--no-browser`.
- **A genuine SQL error mid-stream still stalls a profile.** A row that cannot
  be *used* now costs that row, which is what the NULL and orphan cases
  needed. A row that cannot be *read* — a corrupt page, a column holding a
  type the scan cannot take — still aborts, and since the watermark does not
  move, the next run fails identically. Nothing browser-written should
  produce one. The symptom is a `warning:` line naming that profile on every
  run, `--quiet` or not, and a profile that never gets past it: visible, but
  easy to read as a transient rather than as a stall.
- **Flavour is the schema, not the browser.** Chrome, Chromium, Brave, Edge
  and Vivaldi all land in the `chrome/<profile>/…` identity space, so two of
  them with a profile called `Default` share it. A collision needs the same
  rowid at the same microsecond, so it is remote — but it would drop a visit
  rather than warn.
- **`subtype` uses each browser's own vocabulary**, which nearly but does not
  quite agree: Firefox distinguishes a permanent redirect from a temporary
  one, Chrome does not. Anything filtering on it has to know both.

## Stage 2 — the report. Done 2026-09-08.

`spoor report` turns stored events into a day or a week: which projects, how
many hours, and on what grounds. `--json` for anything that wants to read it,
`--table` (the default) for a terminal.

Nothing was added to the database and no source was touched. Collecting and
reporting are separate commands precisely so that the rules below can be
argued with and re-run over the same events until they stop being wrong.

### What is in place

- `internal/report` — blocks, the attribution ladder, the four numbers, the
  timeline, and both renderers.
- `internal/config` grew a `report` section: `cluster_gap`,
  `attention_window`, `head`, `tail`, `count_background`. Every one of them is
  also a flag, and the flag wins for that run.
- `internal/cli` grew `report`, with `--day`/`--week`, `--json`/`--table`,
  `--timeline` and `--min` on top of the five above.
- `internal/store` grew `EventsBetween`, which hands events back in a total
  order that does not depend on the order they were inserted in.

### The shape of a day

Four rules, all crude, all written by hand, none of them clever:

1. **Blocks.** Events no further apart than `cluster_gap` — ten minutes by
   default — are one block of work. Time inside a block is *active*; the
   pauses between blocks are not time at all. A block never crosses local
   midnight, so a day has the same numbers whether it is asked about on its
   own or as part of a week.

   A block reaches past its own events at both ends: `head` before one that
   opens with a typed prompt, `tail` after one that had a human touch in it
   anywhere, two minutes each by default. Writing a prompt and reading the
   last answer are real time that leaves no trace, and without them a block of
   a single event lasts zero. Both need a person: a block of nothing but agent
   output gets neither.
   Neither end takes more than half the pause it reaches into, so two blocks
   cannot claim the same second whatever the settings; neither crosses
   midnight; and both are attention outright rather than by the window,
   because they exist precisely because a person was typing or reading. These
   two numbers are an estimate of one person's habits rather than a
   measurement, so zero is expressible and gets you back to counting only
   what is on disk.
2. **Cutting.** The stretch between two adjacent events belongs to the project
   of the nearest human touch — a prompt somebody typed, or a page their
   browser recorded — with a tie going to the earlier one. A block holding two
   projects is cut between them and never handed to the larger one, which is
   what makes "at any instant, exactly one project" true by construction
   rather than by hope.

   Nearest *touch*, not nearest event, and that distinction is the whole of
   it. With two or three windows open — the normal way to work with agents —
   the agents you are not talking to keep writing, and taking the project of
   whatever spoke last hands your minutes to whichever agent is noisiest. On
   real data the owner changed 1522 times in a fortnight under that rule and
   132 under this one, and 9% of the hours moved. Nearest rather than most
   recent because a person reads before they answer: the minute before a
   prompt was spent on the thing about to be prompted.
3. **Inheritance.** An event with no project of its own takes the project of
   the nearest *prompt* in its block, falling back to the nearest event
   carrying a project when the block holds no prompt at all — a resumed
   session, or one whose prompt fell in an earlier block. Prompts rather than
   any named event for the same reason as rule 2: browsing is a human touch
   and owns the seconds around it, so naming it after whichever agent spoke
   nearest would let the noisiest one back in through the side door. A block
   where *nothing* is named —
   about half of them, all browsing — looks at the nearest named block on
   either side. Both naming the same project, or one neighbour with nothing
   at all on the far side, and the block takes that name; two neighbours
   naming different projects, and it stays unnamed with both candidates
   printed. A missing neighbour is not a disagreement. Neither rule ever
   inherits from something inherited, so the answer does not depend on the
   order blocks are visited in.
4. **Attention.** A prompt somebody typed and a page somebody's browser
   recorded are moments of human attention; an assistant message, a tool
   result and a subagent's prompt are the machine. Each moment casts a window
   of `attention_window` either side of itself, the windows merge, and active
   time inside them is *attention*. What is left is the agent working alone.

### Four numbers, kept apart

```
attention   active time inside an attention window. The one that is summed
background  active time outside every window: the agent kept a block together
            across a pause that would otherwise have ended it. Its own line,
            named for what it is, and not added in unless asked
agent       this project's window was producing output while you were in a
            different one. Per project, and not part of the day: two agents
            can be busy in the same second
wall        first to last event of a project. Context, never a total: four
            parallel chats give four wall times and one day of attention
```

`attention` and `background` partition the active time. `agent` and `wall` do
not: both are summed per project and both can exceed a day, on purpose.

**`agent` compares sessions, not project names.** One chat window changes its
own `cwd` when a shell command does — measured: one session, 1972 events, two
project names in a day — so comparing names would report "the agent worked
while you were elsewhere" about the window you never left.

The attention window follows the clustering threshold: half of it, unless set
explicitly. At exactly half, two touches leave no background between them
precisely when they are no further apart than the threshold — which gives the
background line one meaning instead of a number of them: the agent held a
block together across a pause that would otherwise have ended it.

One more line joins the summary when `head` or `tail` put time there:
`of which written and read`, and `padding_ms` in `--json`. It is inside
attention already, not beside it. It is called out because it is the only part
of the day that was not measured.

`--count-background` changes what is summed and never what is measured: the
background line is printed either way.

`--timeline` adds the same day as a schedule under the table: one line per
stretch with a single owner, and the pauses between blocks as lines of their
own. It replaces the day-by-day summary, not the table itself. A line ends
where the project changed, so a day of two or three open windows comes out as
a few dozen lines rather than one per event.

### Invariants, proven by tests rather than by eye

| Invariant | Test |
|---|---|
| Attention over all projects sums to the active time, and a day cannot hold more than a day | `TestAttentionSumsToActiveTime`, `TestParallelSessionsCannotExceedADay` |
| At one instant, attention belongs to exactly one project | `TestMixedBlockIsCutNotRounded` |
| Two runs over the same data give byte-identical `--json` | `TestJSONIsByteIdentical`, `TestReportJSONIsByteIdentical` |
| …including when the events arrive in a different order | `TestJSONIsByteIdenticalWhateverTheInputOrder`, `TestInputOrderDoesNotMatter` |
| A day is the same day inside a week | `TestADayIsTheSameInsideAWeek` |
| Reporting never writes to the database | `TestReportDoesNotChangeTheDatabase` |

The parallel-sessions test is the one worth keeping honest: it builds four
projects producing an event every thirty seconds from midnight to midnight —
the "four agents, thirty-two hours" case the whole design exists for — and
asserts the day still holds a day.

### Acceptance

Measured against a real database over the same two-week window the survey
used. The absolute numbers live outside this repository with the rest of the
measurements; the shape of the result is:

| Check | Result |
|---|---|
| `active`, with `--head=0 --tail=0`, against the hours stage 1.5 measured | the same total to the digit, over the thirteen closed days both cover |
| Share of the day from the first block to the last that blocks fill | 37%, from 8% on the quietest day to 68% on the busiest; 34% with the head and tail turned off |
| Time in single-project blocks, before and after inheritance | 31% to 44% |
| Time with no project at all, before and after inheritance | 16% to 3% |
| Time in blocks whose main project holds less than 70% | 27% |
| Of the active time, how much rests on a one-sided neighbour | 9% |

The last three lines are the point of the stage and they say something
uncomfortable: inheritance names four fifths of the unnamed time, and a block
still is not a project.

### What is deliberately not done

- **Conversations in the desktop app's chat tab are not covered, and cannot
  be.** Cowork sessions write to `~/.claude/projects` like any other Claude
  Code session and are read normally. A plain chat has no working directory
  and writes nothing there; the only local trace is the web app's IndexedDB
  cache, which is the text of the conversation and therefore out of bounds
  here. Tested by naming a chat and searching the disk for that name: nothing
  outside that cache. So work done entirely in chat — including through a
  connector — is missing from the day, and it would have no project even if
  it were not, because the project comes from the working directory. It is
  the same shape of hole as a meeting.
- **No dictionary.** A project is still `basename(cwd)` plus inheritance.
  Mapping a domain or a path to a project by hand is the next stage, and the
  reason it comes second is that the busiest browser keys — one search engine
  accounts for a sixth of all browsing — can only be judged once you can see
  what is left unnamed after inheritance.
- **`turn_duration` is still unused.** It is the only honest duration in the
  data, and adding it would double count: the gap between the events around
  it already covers the same wall time.
- **Nothing is written back.** A block cannot be named, split or confirmed;
  that is the terminal interface, one stage further on.
- **Windows between blocks are shown but not stored.** `--timeline` prints
  each one as a line of its own and `--json` carries them as runs marked
  `gap`, so blocks plus windows do add up to the length of the day on screen.
  Nothing about them is written down, though: they cannot be named, confirmed
  or turned into "that was work after all". The survey argues they should be
  records in their own right, and that is still not done.

### Loose ends for whoever comes next

- **The attention window barely separates anything at its default.** At half
  the clustering threshold — five minutes either side, by default —
  background is under a tenth of active time on real data. That is not the
  rule failing: browsing counts as attention, and a person waiting on an
  agent browses, so stretches of ten minutes with neither a prompt nor a
  visit are rare. At one minute either side the split becomes roughly even,
  but it also stops meaning the same thing: two minutes since your last
  keystroke is you reading, not the agent working alone. Whichever number is
  right, the default makes the background line nearly always small, and it
  should not be read as "the agent barely worked alone".
- **Every browser visit counts as a moment of attention**, including a
  redirect and a frame load. Telling those apart needs each browser's own
  transition vocabulary and the two do not agree; on real data it is a
  rounding error, but a page that refreshes itself every minute would hold
  the attention window open all night.
- **A one-sided neighbour names about a tenth of the day, and cannot be
  checked.** Browsing at the edge of a day has a named block on one side and
  nothing on the other, so there is nothing to agree or disagree with it. The
  first version of the rule refused those and left three quarters of the
  unnamed time unnamed for want of a neighbour that could not exist; this one
  accepts them and reports how much it accepted. Neither version is
  verifiable from the data — evening reading really can belong to something
  else, and only the person who did it knows.
- **The report is what made the first-path-segment risk visible, and it is
  real.** Stage 1.5 wrote down that `/reset-password/<token>` is safe while
  `meet.google.com/<code>` is not, because for some hosts the secret *is*
  segment one. Grouping browser events by host and first segment put one of
  those on screen: a Telegram Bot API credential, which lives at
  `api.telegram.org/bot<token>`, had been imported and was printed in the
  evidence column. Nothing is wrong with the truncation — the rule kept
  exactly what it promised to keep — and the ignore list is still the only
  control. What changed is that the risk is now demonstrated rather than
  predicted, and the README says so with a query for finding such hosts.
- **`basename(cwd)` is wrong in both directions at once.** It splits one
  project into two — a checkout and a subdirectory opened separately are two
  names, and worse, a single chat window changes its own `cwd` when a shell
  command does, so one session reports under several names. And it merges
  what should stay apart: two unrelated directories both called `src` or
  `out` become one project. The dictionary has to fix both, and the two pull
  in opposite directions. The full path is in `cwd` already; nothing uses it.
- **`head` and `tail` are an estimate, not a measurement.** Two minutes each
  is what one person believes about their own habits; the clustering
  threshold has a knee in a density curve under it and these have nothing.
  They move the day by 9%. The honest measurement that could replace them is
  in the data already — `durationMs` on `system/turn_duration` — and nothing
  reads it.
- **A block that opens with browsing gets no head.** Typing an address is not
  composing a prompt, so the rule only fires on Claude Code prompts. On real
  data that is why the two together add a little over half of what four
  minutes a block would give.
- **`--min` folds by three columns at once**, so a project stays if it is
  large on any of them. That keeps the "agent worked and you never did" case
  visible, which is the one worth reading, but it does mean the fold catches
  less than a threshold on attention alone would. On real data it takes a
  week from 33 rows to 23.
- **Wall for the unnamed row is the whole day.** It is the first to the last
  unnamed event, which for scattered browsing is breakfast to bedtime. True
  by the definition and useless as a number.
- **A project with events but no time gets a row of zeros.** A single visit
  is a point, and a point has no duration. `--min` folds those away by
  default, which means the honest answer is one flag further from the reader
  than the tidy one.
- **The `--day` and `--week` flags take their date with an equals sign**,
  because both also work bare. Written with a space the date is a leftover
  argument; that is refused with a message rather than quietly reported on
  today, which would have been indistinguishable from the right answer.
- **The clustering threshold has not been re-measured, and cannot be yet.**
  Ten minutes comes from buckets holding four to eleven observations. Taking
  it again needs a longer window than Claude Code keeps, which is the whole
  reason the database accumulates — so the number improves only with time
  passing. Everything else is stated against it: the attention window is half
  of it, and moving it moves the headline number a long way. The sensitivity
  table lives with the measurements outside this repository.

## Stage 3 — projects and attribution. Done 2026-09-08.

Which project an event belongs to is now a dictionary in the config file —
paths, browser keys, branches and page titles — and one level below that, the
subject: the thing that spans weeks. `spoor report --subject NAME` answers how
long one of those has taken over the whole database, and
`spoor report --unmatched` says which line of the dictionary is missing.

Nothing was added to the database and no source was touched. The rules are
applied when a report is built.

### What is in place

- `internal/rules` — the compiled dictionary. Four kinds of rule, a written
  order to break ties, and one function that says whether an event is covered
  at all.
- `internal/config` grew an `attribution` section: `fallback`, `never`,
  `subjects`, `projects`. Regular expressions are compiled while the file is
  read, so a broken one names its line. `never` takes two lists — `keys` and
  `paths` — and still reads a bare list as `keys`.
- `internal/report` grew subjects, the two views above, and `work` on a
  project row.
- `internal/store` — `EventsBetween` now loads `title`, and `Range` says what
  the whole database spans.
- One change to what stage 2 printed: a browser key whose host is an address
  is now written with brackets — `[::1]:3000/app` rather than `::1:3000/app` —
  wherever a key appears. The old form cannot be read back, and a key is no
  longer only printed: it is what the config is written from.
- `internal/cli` grew `--subject` and `--unmatched`.

### Why the rules run at report time

An imported event is never re-parsed. A rule applied at import would therefore
reach only what was collected after it was written, and the dictionary would
be a thing you could only get right in advance.

Applied when the report is built, one edit renames a year of history, and a
rule that turns out to be wrong costs a re-run rather than a re-import. This
is the same reason collecting and reporting are two commands, and it is what
makes the numbers below arguable at all: every one of them was produced by
running the same events through two different config files.

### The four rules and how they are chosen between

| Key | Matches | Against |
|---|---|---|
| `paths` | a directory and everything under it; `~` for home; a trailing `*` for a plain prefix | `cwd` |
| `keys` | `host[:port][/first-segment]` — what the report prints | a browser visit |
| `branches` | a regular expression | `git_branch` |
| `titles` | a regular expression | `title` |

Any one matching is enough, and the most specific match wins whatever order
the file is in: a longer path beats a shorter one, a longer key beats a
shorter one, and a literal beats an expression — where something happened is
better evidence than what a piece of text looked like. Two expressions cannot
be compared, so between them the written order decides, always the same way.

**A path rule covers what is under it**, which is the half of the old guess
that mattered most: on real data one project was spread over four directory
names, and `/tmp` worktrees created per session added a dozen more.

**`never` takes the trace away, not the event.** A rule reading the title still
applies to a key on that list, and that is the point rather than an oversight:
the host of an issue tracker says only "the tracker", while the title says
which board or which repository. Measured: on the tracker in use here, 63% of
titles name the team or the board, and on the GitLab instance 93% name the
repository. That is the whole of the tracker support — no API, no network.

It applies to directories as well as hosts, and there it earns more: a host
with no rule is unnamed, while a directory with no rule is named by the last
element of its path. Without `never.paths` the only way to refuse that guess is
to turn it off everywhere, which also stops a directory nobody has seen before
from appearing as a row of its own. Measured on a real dictionary: silencing
three known directories with the guess left on gives the same numbers, to the
digit, as turning the guess off — and four temporary directories still show up
in `report --unmatched` that the blunt switch had swallowed.

**And a never entry loses to a more specific rule of its own kind.** That is not
symmetry for its own sake: `report --unmatched` prints the home directory as
something to decide about, and pasting it in used to take away every rule
underneath it at once. Now it silences what nothing else speaks for, and the
checkout inside it keeps its name. The floor is per kind, so a silenced host
never reaches a path rule.

**A silenced path names one directory; a project's path names the tree.** The
asymmetry is deliberate and it comes from the same paste. Silencing says "I
looked at this directory and it names nothing", which is a statement about the
one directory somebody looked at — so what is underneath keeps the guess and
stays in the list of things to decide about. Measured on the real dictionary:
under the subtree reading, silencing the home directory swallowed a directory
holding 1104 events and forty minutes, which then existed in no list at all.

The tree is two entries — `[~/scratch, ~/scratch/*]` — because a trailing `*`
is a plain string prefix here as everywhere else in the config, and `~/scratch*`
alone would also take `~/scratchpad`. That is a sharper edge than it looks on a
control whose whole purpose is to silence no more than was meant, and the docs
say so rather than offering the one-character version.

### Subjects: the second level, and the last

A subject is an episode, a level, a feature, a ticket. It is written like a
project rule with a name; with **no** name, the first capture group of the
expression that matched becomes the name, so

```yaml
subjects:
  - titles: '\b([A-Z]+-\d+)\b'
```

gives every ticket a subject of its own without listing any of them.

There is no third level on purpose. A tree of any depth would make every
report say which depth it was answering about; the question people ask is how
long one thing took.

**A subject never inherits.** A project can take its name from the block
around it. A ticket number in a page title says that page was about that
ticket and says nothing about the hour that followed, so a subject holds the
stretch of the day around its own traces and no more — by exactly the rule
projects are cut by, the stretch belonging to the nearest human touch. The
consequence is that the subjects of a project do not add up to it, and the
report says so on the line above them.

### Acceptance

Measured on the same fourteen-day window as stages 1.5 and 2, by running the
same database through two configs: an empty one and the author's dictionary of
about a hundred lines. Absolute numbers live outside this repository with the
rest of the measurements.

| Check | Without rules | With the dictionary |
|---|---|---|
| Events carrying a project of their own | 78.7% | 83.6% |
| Browser events carrying one | 0% | 23.3% |
| Events with no project after inheritance | 0.8% (256) | 0.8% (258) |
| Active time with no project | 2.7% | 2.6% |
| Project rows over the window | 44 | 17 |
| `active` in total | identical to the millisecond | identical to the millisecond |

Browsing is the whole of the first two lines: every Claude Code event already
had a name from its working directory, and no browser event had one at all,
so the dictionary is the only thing that can give one.

The row count is the result, and the two lines above it are the honest
disappointment. The dictionary did not make the report name more: inheritance
was already leaving 0.8% of events and 2.7% of the active time unnamed, and
there was nothing there to take. What it changed is that the names are
**right**. Of the
44 rows, more than half were fragments: subdirectories of one project, a
temporary worktree, the last element of a path that two unrelated checkouts
share. One project came out sixteen hours larger, having been split across
four other names and a browser host nothing had claimed.

The share with no project after inheritance did not move — and the count
behind it went **up**, from 256 events to 258, while the time behind it went
down from 2.7% to 2.6%. The two directions are the same effect: naming more
events by rule gives the neighbours of an unnamed block more to disagree
about, and a block whose two neighbours disagree stays unnamed on purpose.
Better attribution makes the remaining ambiguity visible rather than smaller,
and two events is what that cost here.

### What is deliberately not done

- **No rule combines two fields.** A rule is any-of, never all-of: there is no
  way to say "this host **and** this title". It was not needed on real data —
  the title alone is specific enough where it matters — and every attempt to
  write the syntax moved the twenty-line target further away, not nearer.
- **Nothing is written back.** The dictionary is edited by hand. Naming a
  block in an interface and having that answer stored as a rule is the
  terminal interface, one stage on; `--unmatched` is the part of it that can
  be had without one.
- **`work` is carried and not summed.** It reaches `--json` and stops there.
  What a work/personal split should look like in a report is a question for
  whoever wants one; the flag exists so the question can be asked of real
  data.
- **`turn_duration` is still unused.** It was looked at again for this stage
  and it cannot yet replace the head and tail estimate: 244 events carry one
  in a database of 83,000, and what it measures is a whole turn rather than
  the time somebody spent typing. It stays a loose end.

### Loose ends for whoever comes next

- **A subject collects only the time around traces that carry it, and that is
  most of what there is to know about the second level.** Measured on a real
  dictionary over a fortnight: subjects covered 37% of the time their *projects*
  covered, ranging from 86% down to 20%. (The 3.5% quoted in README for issue
  keys is a different denominator — the time that issue was worked on, not the
  time its project holds.) The clearest case is a project whose
  subject rule is *identical to its own* — it still covered only 44%, because
  the browsing inside its blocks takes the project by inheritance and cannot
  take the subject.

  The ceiling was measured too, by promoting every subject to a project so that
  it inherited: coverage went from a third of the time to nearly all of it. The
  rule was left as it is anyway. What raises coverage honestly is leaving the
  same name on the folder, the branch and the ticket — a person can do that in
  a week, and a rule that guesses cannot be argued with afterwards.
- **Time inside a block follows the nearest human touch, so a rule pointed
  where your touches do not reach gets traces and little or no time.** One
  directory in the measured data held 2983 events and exactly two prompts, both
  from subagents: the person prompted from the parent and the agent worked in
  the child. As a subject it produced no time at all; as a project it would get
  a row with a wall time and no hours.

  It is worth being exact about which shape this is, because the other shape
  behaves differently: a block with *no* touch in it at all — a session resumed
  with its prompt in an earlier block — keeps each event's own name and counts
  the time as background. The loss happens only when the touches are elsewhere
  in the same block.

  Nothing in the report says this out loud. The "spent time with an agent
  working and none with you" line does not fire here: `liveSessions` compares
  sessions rather than project names, and prompting from the parent while the
  agent works in the child is one session — which is exactly the `cd` case that
  comparison exists to survive. The line needs a second window to appear.
- **A capture group prints whatever it captured.** The subject name comes out
  of the page title, so an expression like `(.*)` would put a page title on
  screen — and the title of a search results page is the query. `([A-Z]+-\d+)`
  captures a key and not the query around it. Nothing caps or checks this: it
  is the author's own expression in the author's own config, and the report
  does what it was told.
- **`EventsBetween` now loads `title`.** It was deliberately not loading it,
  and the comment saying so has been replaced with one saying why it does. No
  renderer prints it; `TestNeitherRendererPrintsATitleOrALabel` builds a report
  with a distinctive title and greps every view — day, timeline, subject,
  unmatched, both formats — for it. That test is the whole of the guarantee.
- **`--unmatched` prints browser keys, and a first path segment is sometimes a
  secret.** The same hazard the evidence column already had: a meeting code, a
  bot token, a link shortener's slug. Nothing new is exposed — the report has
  printed keys since stage 2 — but this view sorts by time and shows more of
  them, so it is the command most likely to put one on screen.
- **The same subject can appear under two projects.** A ticket page visited
  during a block owned by one project and again during a block owned by
  another produces two rows with the same subject name. That is the model
  working — a subject is inside a project — but a person reading it will
  expect one row and will have to add them up.

  `--subject` does add them up, and there the same behaviour is a trap: two
  projects that each have a subject called `release` are two different things,
  and one headline number covers both. The per-project table underneath is the
  only thing that shows it. A way to ask about one project's subject —
  `--subject payments-api/release` — is the obvious fix and is not written.
- **`--subject` and `--unmatched` load the whole database into memory.** Both
  default to every day there is, and a report is built over all of them: on a
  database of 83,000 events that is under a second and a few tens of
  megabytes, but the database is meant to accumulate for years, and titles are
  now loaded with the rest. Narrowing with `--week` is the workaround; a
  streaming or windowed build is the fix nobody has needed yet.
- **Time in `--unmatched` is not the report's time.** It is the stretch that
  begins with each event, charged to whatever was on screen then, rather than
  to the project the stretch counted for. It answers "how much of the day
  happens around this host", which is the question a discovery list should
  answer, and it does not add up to anything in the report.
- **A dictionary of twenty lines does not close ninety per cent, and cannot.**
  About a hundred lines named 83.6% of events, and the rest is mostly not
  addressable by more lines: over half of what is left is on keys the
  dictionary refuses on purpose — one search engine is a sixth of all browsing
  — and the remainder is a tail of 303 keys over a fortnight, most seen once.
  The block around them is what names those, and it does. The twenty-line
  figure holds for the *directories*, which is where it came from.
- **`fallback: cwd-basename` is on by default**, so a directory nobody has
  written a rule for still gets a name, and that name is still wrong in both
  directions. It is the right default — adding a first rule must not take a
  name away from everything else — but it does mean the guess never fully goes
  away unless it is turned off, and `--unmatched` is the only thing that says
  which rows are still resting on it.
- **A project name is a string, and two rules may share one.** That is how a
  project written twice in the file is one project. It also means a typo in
  the second entry is a second project, silently.
- **Subjects have no dictionary of their own.** They exist only where a rule
  found one, so a project with no subject rules has no subjects at all and
  `--subject` has nothing to answer about. The message says so and lists what
  does exist.

## Stage 3.5 — the calendar. Done 2026-09-09.

Meetings are in. The day no longer shows a pause where a call was, and this is
the first stage in which `spoor` can open a socket at all.

### The measurement

Day coverage over the same fortnight the earlier stages used: **37.3% → 42.9%**,
active time 99.02 h → 113.76 h. The numbers themselves live outside this
repository, with the rest of the survey: the database is public, absolute
counters with dates are not.

Two of those numbers matter more than the headline:

- **The "before" column was recomputed with this binary, calendar switched
  off, and matched the previous stage's table on all fourteen days to two
  decimal places.** That is the check that teaching the report about intervals
  changed nothing for sources that produce points. It is worth re-running the
  same way after any change to `splitBlocks` or the stretch tiling.
- **Gap time sitting on top of a meeting: 14.11 h before, 0.00 h after**, on
  every day that had meetings. That is the stage's acceptance criterion
  measured literally rather than eyeballed.

`span` did not move on any day, which is the useful negative result: meetings
fall inside the envelope of the day's other traces, so the calendar filled
holes rather than stretching the denominator.

### Six expansion bugs, all found by review

Worth naming, because every one of them returned a plausible number rather than
an error, and every test written before the review happened to start its series
on a day where the bug did not show. They are locked by
`TestExpansionCasesFoundByReview`.

- **A period reaches back before DTSTART.** A weekly period is expanded across
  the whole week its base falls in, and that week begins up to six days
  earlier — so `FREQ=WEEKLY;BYDAY=MO,WE,FR` starting on a Wednesday invented a
  meeting on the Monday before the series existed. Worse, the phantom was
  counted before the window filter, so it consumed one of COUNT's occurrences
  and a real one fell off the end.
- **The walk stopped on the period's base.** For the same reason — occurrences
  can sit before their base — a weekly or yearly series ended one period early
  at the far edge of the window, silently. `periodFloor` now gives the earliest
  moment a period could hold, and the walk stops on that.
- **`BYMONTH` with no day rule expanded to every day of the month.** "Every
  January" meant thirty-one meetings. The day now comes from DTSTART, which is
  the same defaulting the no-BY path already did.
- **A yearly ordinal `BYDAY` was counted per month.** "The first Monday of the
  year" was the first Monday of all twelve. The ordinal scope is now the year
  when the rule is yearly and names no months.
- **The fast-forward charged empty months against COUNT.** A monthly meeting on
  the 31st ended early, and by how much depended on where the window started —
  which is exactly what the comment above it promised could not happen. Only
  the fixed-length frequencies skip ahead now.
- **A yearly rule on 29 February landed on 1 March** three years in four,
  because `AddDate` normalises. A date that does not exist has no occurrence.

### A second review pass, and what it found

The first pass found six expansion bugs; the second found one more of exactly
the same shape, one of a different kind, and a handful of refusals that were
not being made. Worth
recording because the pattern is the lesson:

- **`BYMONTHDAY` was applied only in the monthly branch.** RFC 5545 makes it a
  limit for `DAILY` and below, so `FREQ=DAILY;BYMONTHDAY=15` — "the 15th of
  each month" — expanded to every day of every month. This is the `BYMONTH`
  bug of the first pass, one BY-rule along, in the branch that fix did not
  reach. Forbidden with `WEEKLY`, so that combination is refused instead.
- **A `DURATION` of 365 days or more was read as no duration at all.** The end
  was parked as an offset from the zero time and recognised by "is the year 1
  or earlier"; `P365D` lands in year 2. The event was then filed under a
  counter that says "with no length", which is not what happened. It has an
  explicit flag now, and no sentinel.
- **A feed cut between two events reported success.** The refusal only covered
  a cut inside a `VEVENT`. A login page served with 200 was likewise
  indistinguishable from an empty calendar. The envelope must now be present
  and closed.
- **A meeting had no upper bound.** One bad `DTEND` made a single event claim
  every remaining hour of its day as attention. Anything over a day is refused
  and counted.
- **`EXRULE`, a second `RRULE` and `RANGE=THISANDFUTURE`** were each silently
  half-applied. All three now cost the event they are on: it is dropped,
  counted as unreadable and named in a warning, and the rest of the feed still
  imports. A nested `BEGIN:VEVENT` used to overwrite the event it opened
  inside, losing one with no counter and no note; that one refuses the whole
  feed, because it is the file being wrong rather than one entry in it.

### What is in place

- `internal/source/calendar` — an iCalendar parser, recurrence expansion, and
  the one network path in the program.
- `internal/text` — the string repair that used to live inside the browser
  source. It moved because there has to be exactly one of it, and the calendar
  is the second source reading bytes somebody else wrote.
- `spoor add-calendar <id>` — writes the feed URL into its own file with the
  right permissions, reading it from the terminal without echoing it.
- `spoor ingest --no-calendar` — the switch that guarantees no network
  whatever the config says.
- The report understands intervals; see below, because that was the larger
  half of the stage.

### A meeting is an interval, and the report was built for points

This is the part to read before changing anything in `internal/report`.

Every other source produces moments in time, and the day is made of the
stretches *between* them. A meeting carries its own length, and it is the only
event that does: nothing is written to disk during a call, so without that
length the hour does not exist at all.

Five things had to change, and each was a real bug rather than a rename:

- **`entry` gained `end`.** It equals `t` for everything but a meeting.
- **The pause that ends a block is measured from the running maximum end**, not
  from the previous event's timestamp. Measured from the start, an hour-long
  call reads as an hour-long pause and cuts the block in half.
- **A block ends at its greatest end.** A call that was still running when the
  last page of the block was opened decides where the block stops — and with
  it the span the coverage number divides by.
- **`attentionWindows` sorts before merging.** `windows.add` only ever compares
  against the last interval, which was sound while every interval came from a
  point offset back by the same half-width. A meeting at 12:00 arrives before a
  prompt at 12:02 whose window opens at 11:57, and the three minutes in front
  were silently dropped. `TestAttentionBeforeAMeetingIsNotLost` fails without
  the sort; it was checked by removing it.
- **A project's `wall` reached only the last timestamp.** It is "first trace to
  last", and a meeting is the one event that lasts, so a project whose day was
  one long call reported half an hour of wall against an hour of attention —
  wall shorter than the time inside it. Found by the documentation review, not
  by the code review: the number was consistent with its own definition and
  wrong against the report beside it.

`ownSpan` is the one place that decides which durations are spans.
`turn_duration` deliberately is not one — the turn already has events at both
ends, so counting it would count the same seconds twice.

### What is stored, and what is read and thrown away

Start, end, title. The title goes in `title`, the length in `duration_ms`, and
the calendar's own id in `entrypoint` — the column that already meant "which
instance of a source wrote this", alongside `chrome` and `cli`.

Attendees are read in exactly one place, to answer whether *you* declined, and
never written. The description, the location, the organiser and the conference
link are not read at all. The raw feed is parsed from memory and never reaches
a disk: caching it would put more on the machine than the database is allowed
to hold.

### The identity, and the bug it prevents

`external_id` is `<calendar>/<UID>/<occurrence>`. All three parts are needed:

- an iCalendar UID is unique **within** a calendar, not across two. An
  invitation keeps the organiser's UID in every attendee's copy, so subscribing
  to a work calendar and a shared one that both hold the same meeting produces
  one UID twice. With a dedup key of UID alone the second is dropped on the
  unique index — no error, no warning, an import reporting success while
  holding half the meetings;
- the occurrence separates the instances of a repeating series.

Two calendars sharing an *id* would do the same damage, so a repeated id is a
fatal config error rather than a warning.

### Bounded on purpose, because the bytes are somebody else's

- The response is capped **after decompression**: `net/http` asks for gzip and
  unwraps it, so `Content-Length` says nothing about what it expands to.
- One content line is capped.
- Recurrence expansion is capped twice — periods examined and occurrences
  produced. `RRULE:FREQ=SECONDLY` with neither `COUNT` nor `UNTIL` is a legal
  rule that never ends, and it arrives over the network.
- Hitting any cap is reported as a warning. A series that stopped early is
  hours missing from a day, which is the one failure this tool can least
  afford.
- A rule the parser does not implement (`BYSETPOS` and friends) is refused and
  reported rather than half-expanded. Guessing puts a meeting on a day it never
  happened.

### The URL is a credential, and Go's defaults leak it

Three of them, each verified in the standard library source rather than
recalled:

- **`net/url.Error` prints the whole URL** (`%s %q: %s`). The ordinary
  `fmt.Errorf("...: %w", err)` therefore puts a private calendar address on
  stderr the first time a connection is refused, and from there into a
  screenshot or a pasted issue. `scrub` keeps the cause and drops the address.
- **A redirect sends the previous URL as `Referer`**, path included, and Go's
  default policy allows ten hops to any host. `checkRedirect` deletes the
  header — which works because net/http assigns it immediately *above* the
  `CheckRedirect` call, not after.
- **Transparent gzip**, covered above.

There is no flag for the URL and `add-calendar` will not take it as an
argument: `/proc/<pid>/cmdline` is world-readable by default.

### What it skips, and why the counts are printed

Cancelled, all-day, `TRANSP:TRANSPARENT`, declined-by-you, zero-length and
longer-than-a-day entries are dropped, along with events carrying no usable
UID, and `ingest` prints how many of each.

A repeating event whose rule this parser will not guess at gets a counter of
its own — `not expanded`. It had none for four review passes: the event was not
unreadable, so it fell through every category and a whole series contributed
nothing with only a warning to say so. That is the exact shape this block of
counters exists to make impossible, surviving four passes inside the mechanism
built to catch it. A source that
silently discards two thirds of a file looks exactly like a source that works.

All-day entries are the one to watch: importing a holiday as a twenty-four
hour interval would claim the whole day as attention and make the coverage
number meaningless.

### Loose ends for whoever comes next

- **Only one calendar has ever been read.** Multiple calendars are in the
  identity, in the config and in the tests, but on synthetic fixtures only. The
  real check is the day one meeting arrives from two subscriptions.
- **The refusal branch has never fired on real data.** No rule in the feed
  measured needed `BYSETPOS`, so the "reported, not expanded" path is covered
  by tests and nothing else.
- **A meeting gets its project from a title rule or from its neighbours**, like
  a browser cluster. There is no way to say "this whole calendar is that
  project". Whether that is missing is a question for the interface stage,
  when it becomes visible how many meetings are unnamed.
- **`TRANSP:TRANSPARENT` is a judgement.** It is the calendar's own word for
  "this does not make me busy", and focus-time blocks are often written that
  way. If those turn out to be real work, this is the line to revisit.
- **The expansion window is 400 days back and 31 forward.** Re-reading is free
  on the dedup key, but a database rebuilt from scratch over a longer history
  than that silently gets nothing older.
- **The same bug has now been found at three frequencies, which is a shape
  problem rather than three mistakes.** "This period says which month but not
  which day, so the day comes from DTSTART" was missing under MONTHLY with
  `BYMONTH` (pass one), under DAILY with `BYMONTHDAY` (pass two) and under
  WEEKLY with `BYMONTH` (pass three). Each time the answer expanded to every
  day of the month and each time it looked like a plausible number. They are
  fixed and pinned, but the defaulting lives in three separate branches of
  `occurrencesIn`, so a fourth frequency is one `BY` combination away from the
  same fault. The structural fix is one place that decides which days of a
  period an occurrence may fall on, with the frequency choosing the period
  rather than the rule; that is a rewrite of `occurrencesIn` and wants its own
  change, not a fourth patch.
- **A meeting that was moved is never corrected.** The dedup key is
  `<calendar>/<UID>/<occurrence>` and inserts ignore conflicts, which buys
  idempotence and nothing else: there is no delete path anywhere in
  `internal/store`. A single meeting rescheduled from 10:00 to 14:00 keeps its
  UID, so the new time is dropped as a duplicate and the database keeps the old
  hour. A whole series moved changes every occurrence key, so the new
  occurrences are inserted and the old ones stay — and both are counted. A
  meeting cancelled after it was imported is skipped at parse time and its row
  survives. This is a real hole, not a deliberate boundary, and it is the
  "quietly too big" kind. The shape of the fix is a per-calendar sweep of the
  window: delete this calendar's rows in [from, to) and rewrite them, which is
  a change to how the store is used rather than to the source.
- **A meeting is cut at midnight rather than split.** It belongs to the day it
  started on, and the part after midnight is dropped. Counting it on both days
  would be worse and splitting an event across days changes what a day is, but
  it does mean a call from 23:00 to 02:00 reports three hours as one.
- **`ingest` prints its warnings on stdout, and this file says they belong on
  stderr.** That is older than the calendar — every source has always done it —
  but the calendar is what made it matter: unreadable events, unexpanded rules,
  a truncated feed and "configured but not enabled" all flow through there now,
  mixed in with the counts a person reads to decide whether a source worked.
  Deliberately not changed here: it moves the browser's and Claude Code's
  output too, and a change to what every source prints should be reviewed on
  its own rather than arriving inside a stage about meetings.
- **`golang.org/x/term` is a new dependency**, for reading the URL without
  echoing it. Pure Go, no cgo, but it is the third dependency in a project that
  had two, and it is **pinned to v0.27.0 on purpose**. `@latest` is v0.46.0,
  whose own go.mod requires Go 1.26 — taking it raises the minimum Go version
  for everybody in exchange for not echoing one prompt. Pinning the older
  `golang.org/x/sys` it wants instead dragged `modernc.org/sqlite` back from
  v1.58.0 to v1.35.0. v0.27.0 costs neither: the go directive stays at 1.25.0
  and `x/sys` stays where SQLite put it. Check both before bumping it.

## Next: confirming the day

The terminal interface: walk the blocks, name what the rules could not, and
store the answer as a rule so the question is asked once.
