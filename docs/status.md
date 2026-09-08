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

## Next: the report

Clustering events into blocks with the 10 minute threshold from the survey,
in the config rather than in a constant. Three times kept apart — wall,
attention, agent — and a browser-only cluster inheriting its project from its
neighbours in time, which is what half the clusters of a day need before any
of them can be named.
