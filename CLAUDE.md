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
   out at all. There is exactly one outbound path — the calendar source, which
   fetches an iCalendar feed — and it is off until two settings and a file are
   written by hand; `ingest --no-calendar` turns it off whatever the config
   says. README carries a Network section above the install instructions, and
   any future path (an LLM mode, say) joins it under the same terms.
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
internal/config      the optional YAML file under $XDG_CONFIG_HOME
internal/event       the record every source produces
internal/store       SQLite: schema, inserts, queries
internal/rules       the dictionary: which project an event is, and which
                     subject inside it. Compiled from the config, applied
                     when a report is built
internal/report      blocks, attribution, the four numbers, both renderers
internal/source/…    one package per source: claudecode, browser, calendar
internal/text        string repair: valid UTF-8, and nothing that would make a
                     value display as something it is not. One of it, shared by
                     every source that reads somebody else's bytes
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
- **A rule about what gets stored is written down in four places.** The code,
  the column comment in `schema.sql`, the field comment in `event.go`, and
  `docs/status.md` — and if a reader is meant to verify it, the check in
  `README.md` makes five. Change them together and grep for the old wording
  afterwards. Two separate review rounds caught a rule that had been
  tightened in the prose and left stale in the two column comments, and
  `schema.sql` is the one a user reads with the database open.
- **Stdout is the answer; anything else goes to stderr.** `report --json` is a
  document a program reads, so one warning printed on stdout makes it
  unparseable — including for the reproducibility check README asks people to
  run. Warnings, notes and "nothing found" belong on stderr, and a command that
  finds nothing still prints an empty document rather than a sentence. This was
  got wrong once already, in the stage that added the first warning to
  `report`.
- **A setting that can quietly do nothing ships with the check that says so,
  and the check covers every list the setting touches.** A config line that
  parses, looks right and never fires is the worst failure this file has: it is
  invisible from the report, and it survives every test that does not look for
  it. The stage that added the dictionary built `Problem` for exactly this and
  then produced three cases it did not cover — a key the report printed and the
  parser could not read back, a silence that swallowed a subtree, a rule made
  unreachable by a refusal of equal specificity. Two were found by review and
  one by measurement, none by a test. So: when a mechanism gains a second list,
  the check gains it in the same change, and the message names the role of what
  it is talking about, because a project and a subject may share a name.
- **Update `docs/status.md` at the end of a stage.** In a month the context
  will be gone.
- **English in this repository** — code, comments, docs, commit messages.
  The audience is international.

## What the dictionary is for, and what it must not become

- **Rules run when a report is built, never at import.** An imported event is
  never re-parsed, so a rule applied at import would reach only what was
  collected after it was written. Applied at report time, one edit renames a
  year of history. The `project` column keeps whatever the source guessed;
  the dictionary overrides it, and the two are allowed to differ.
- **Deterministic, and that is the whole of it.** A literal path, a literal
  browser key, a regular expression, and a written order to break ties. No
  similarity, no scoring, no model. The most specific match wins whatever the
  file order, and a literal beats an expression: where something happened is
  better evidence than what a piece of text looked like.
- **A fat key must give no project rather than a wrong one.** One search
  engine was a sixth of all browsing measured. `never` is for those, and for
  directories nobody has decided about — it takes the *trace* away, not the
  event, so a rule reading the page **title** or the **branch** still applies.
  That is deliberate: the host of an issue tracker says only "the tracker", and
  the title says which board or repository. It is what replaces an API, and it
  must stay that way round.
  A never entry **loses to a more specific rule of its own kind**, and the floor
  it raises is per kind. And a silenced path names **one directory**, where a
  project's path names the tree. Both come from the same paste: `--unmatched`
  prints the home directory as a candidate, so a subtree there took away every
  rule underneath it and swallowed a directory of 1104 events out of every
  list at once. The tree is two entries — `[~/x, ~/x/*]` — because
  `*` is a plain string prefix here as everywhere else, and `~/x*` would take
  `~/xylophone` too.
- **Two levels. There is no third.** A project and a subject inside it. A
  subject takes its time by the same rule as a project and **never inherits**
  from a neighbouring block, so the subjects of a project do not add up to it.
- **The guess stays as a fallback.** `basename(cwd)` is wrong in both
  directions at once and it is still the default for anything unmatched,
  because a first rule must not take a name away from everything else.
  `report --unmatched` is what stops that being invisible; it is the
  maintenance loop, and anything that makes the dictionary harder to grow
  line by line is the wrong change.

## What the calendar source knows about its own format

Measured against a real feed and seven rounds of review. Ignoring one of these
produces a wrong number rather than an error, which is why they are here.

- **The URL is a credential, and three of Go's defaults work against that.**
  `url.Error` prints the whole URL, so a wrapped transport error puts it on
  stderr — `scrub` replaces it. A redirect sends the previous URL as
  `Referer` — `checkRedirect` deletes the header. And `net/http` asks for gzip
  and unwraps it, so `Content-Length` describes the compressed body and bounds
  nothing: that one is **not** turned off, it is answered by capping the
  stream *after* decompression. Replacing that cap with a `Content-Length`
  check would reopen it. Never assume a fourth default is not waiting.
- **Expand the repetition rules; never half-expand one.** A rule the parser
  does not implement is refused and counted, because a guessed rule puts a
  meeting on a day it never happened. The same bug — "this period names a
  month but not a day, so the day comes from DTSTART" — has been found under
  three different frequencies; if you touch `occurrencesIn`, assume there is a
  fourth.
- **A UID is unique within a calendar, not across two.** An invitation keeps
  the organiser's UID in every attendee's copy, so the identity has to be
  calendar + UID + occurrence or a second subscription silently drops its
  meetings on the unique index.
- **One bad event costs that event; a bad file costs the file.** A property
  this parser cannot use is recorded on the event and counted. Structural
  damage — an event inside an event, a download that stopped — refuses the
  feed, because that is the file being wrong rather than one entry in it.
- **Everything is bounded, including the complaints.** Response size after
  decompression, line length, UID, expansion periods and occurrences, and how
  many kinds of warning one feed may produce. A limit that is reached is
  always reported.
- **Attendees are read in one place and never stored**, to answer whether the
  user declined. Description, location, organiser and conference link are not
  read at all.

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
- **Cowork sessions in the desktop app write these logs; the chat tab does
  not.** Everything in `~/.claude/projects` has a working directory, whatever
  wrote it — `cli`, `claude-vscode` or `claude-desktop`. A conversation in the
  plain chat tab has no working directory and writes no line here at all;
  tested by naming a chat and looking for the name, which turns up only in the
  web app's IndexedDB cache. That cache is conversation content, so it is out
  of bounds for this project by its own rule, and there is no other local
  trace. A connector used *inside* a session with a working directory is
  recorded normally — it is the chat tab that is invisible, not any particular
  kind of work.
- **`cwd` is not constant within a session.** A `cd` inside a shell command
  moves it, so one chat window reports its work under two or more project
  names. Measured: one session, 1972 events, two names in a day; another, 718
  events under four. Anything comparing windows — "was the agent working while
  you were elsewhere" — has to compare `sessionId`, never the project derived
  from `cwd`.
- Claude Code **deletes its own logs after 30 days**. The database accumulates
  rather than being rebuilt on demand; an imported event must outlive the file
  it came from. There is a test for exactly that.

## What the browser source knows about its own format

Also measured, on a real Chrome profile. Same rule: ignoring one of these
produces a bug.

- **Never open the original as a database.** Both browsers hold their history
  open and lock it. Read the file through byte for byte — *and* its
  `-journal` / `-wal` / `-shm` sidecars — into a private temporary directory,
  open the copy, delete it. Copying only the main file can read pages of a
  transaction that was rolled back.
- **The copy must be opened read-write.** SQLite cannot roll a journal back
  in a database it may not write, and that rollback is the whole reason the
  sidecars were copied.
- **Chrome counts microseconds from 1601-01-01**, Firefox from the Unix
  epoch. The offset is `11644473600000000`.
- **`visits.id` restarts at 1 when the history is cleared.** The dedup key is
  therefore flavour, profile, visit time in microseconds, and id — not the id.
  Milliseconds are not enough: a redirect puts several visits in one.
- **Nothing below the first path segment is ever stored.** Not the query
  string, not the fragment, not the second segment. That is where tokens and
  search terms are, and `/reset-password/<token>` puts a token in exactly the
  second segment. Take the segment from the *decoded* path so `%2F` counts as
  a separator, then keep a set derived from RFC 3986 §3.3 — letters, digits,
  marks, `-._~!$'()*+:@` and a space — and end the segment at the first thing
  that is not. Derived, not equal: four sub-delimiters removed (`;`, `=` and
  `,`, which the section names itself, plus `&`), a space added because it
  can only arrive escaped, and ALPHA/DIGIT read as Unicode categories since
  the path arrives decoded. **Allow-list, and taken from the
  grammar**: the deny-list version had to be extended three times as reviews
  found `?`, `jsessionid` and UNC paths, and a hand-picked allow-list grew
  the same way, one character per person's data. Cut, never delete: deleting
  turns `a%0d%0ab` into `ab`, a name that never existed.
- **Every value carrying bytes from the browser goes through one
  `sanitise`.** Not the two or three that come to mind: `host`, `path_head`,
  `title` and the profile name inside `external_id`. The rest — `type`,
  `subtype`, `entrypoint`, `port` — are constants of the package or digits,
  and if that ever stops being true they join the list. A path is
  percent-encoded bytes and `/caf%E9/` is Latin-1; a profile name is a
  directory name, which on Linux is also bytes. SQLite stores such bytes and
  then refuses to return the column, so one row makes `SELECT *` fail
  outright rather than answer wrongly — and `SELECT *` is what the README
  calls "the whole record". This was got wrong twice, each time in a column
  the previous fix did not reach, which is why the test now asserts over
  every string field on the event — including the six this source never sets
  — rather than the ones anybody would think of.
- **Canonicalise a host and an ignore entry through the same call.** An
  address has several valid spellings (`::1`, `0:0:0:0:0:0:0:1`); each side
  would otherwise pick its own and neither would ever warn.
- **Keep the port**, and only when it says something: `localhost` alone
  accounted for dozens of ports on one real machine, so without it every dev
  server is the same project — but a scheme's own default port (443, 80)
  means nothing and would split one host into two keys.
- **A row that cannot be used costs that row.** Every column but the keys is
  nullable in somebody else's database, and a visit can outlive the `urls`
  row it points at. Returning an error instead discards the batch already
  read, leaves the watermark where it was, and fails identically forever —
  one bad row would mean the profile is never imported again.
- **Chrome's `visit_duration` is not a duration of attention.** It runs until
  the tab navigated away, so it sums to twenty-five times the length of the
  period measured. It is not stored, and putting it in `duration_ms` would
  invite the reporting stage to add it up.
- **Incremental by timestamp, not by offset**, because a history database is
  rewritten in place — and with a week of overlap, because a history does not
  only grow forwards. Sync writes another device's visits with their original
  timestamps, and a corrected clock inserts rows behind the newest one. The
  dedup key makes the overlap free; without it those rows are read never and
  nothing says so. **A visit dated in the future must not move the
  watermark** — import it, ignore it for the mark — or one wrong clock stops
  the profile importing anything until that date arrives.
- **`title` is the field to worry about**, not `raw_text`. A quarter of the
  browser events measured were search result pages, whose title is the query.
  The ignore list in the config is the only control there is, and it runs
  before the insert.
