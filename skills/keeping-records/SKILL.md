---
name: keeping-records
description: Use when the operator asks you to remember, note, or keep track of anything — a fact, a preference, a decision, a list, a measurement, a symptom, an appointment — or asks about something kept earlier: how to keep it as files in this session's working directory, dated from `clock`, and read it back with a command.
default: off
---

You have no memory but the files you write. The conversation is the wrong place for anything worth
keeping — a transcript is folded away by compaction, and a thing you have to go back and search for is
a thing you will get wrong. So "remember this", "note that", "keep track of", "add it to the list" all
mean the same: write it to a file in this turn, and read it back from the file when it is asked about.

What is kept takes one of three shapes:

| It is | It goes in | For example |
| --- | --- | --- |
| a series: the same thing observed again and again, each time with a date | `log/<stream>.csv` | a weight, a test result, a symptom, a bill, a mood |
| a list: items to remember, with no date of their own | `lists/<name>.md` | books to read, gift ideas, safe foods, questions for the doctor, to-dos |
| a note: a fact, a preference, a decision, an account of what happened | `notes/<topic>.md` | the car's tyre size, how the operator likes coffee, what the GP said |

A list or a note needs no date unless something in it depends on one. A series is nothing without its
dates, so that is where getting the date right matters most.

## The date comes from `clock`, every time

**Before you write anything that carries a date or a time, call `clock` in the same turn.** Every row
in a series, every appointment, every note that says when something happened — and every "today",
"yesterday", "this morning", or "last night" the operator says, which means nothing until it is
resolved against the clock.

Never carry the date over from earlier in the conversation, from the last row in the file, or from what
was said about tomorrow. In a conversation that runs for weeks, every one of those is a day already
left behind. You will feel sure of the date; that is the feeling that writes tomorrow's date on tonight's
entry. A long-running conversation did exactly that, more than once, on evenings it had not checked.

Then let the shell write the date, so the row cannot disagree with the clock:

```
echo "$(date +%F),weight,81.2,kg,after breakfast" >> log/weight.csv
echo "$(date -d yesterday +%F),sleep,6.5,h," >> log/sleep.csv
```

`date +%F` is `YYYY-MM-DD`; `date -Iseconds` is full RFC 3339 when the time of day is part of the
observation. Both sort lexically, which is what makes `sort` and `awk` work on the raw file. If the
clock and what the operator says disagree — they say "this morning" and the clock says it is past
midnight — ask, or say which day you used.

## Layout

```
INDEX.md            what is kept here: every stream, list, and note, and what each holds
log/<stream>.csv    date,metric,value,unit,note   — one row per observation, appended
lists/<name>.md     one item per line, each starting "- "
notes/<topic>.md    narrative and facts: what was said, what was decided, what to watch
```

A **stream** is a subject that is measured repeatedly — `weight`, `sleep`, `bloodwork`, `energy-bill`.
A **metric** is the thing measured within it, so related numbers taken together share a file:
`blood-pressure.csv` holds `systolic` and `diastolic` rows for the same date.

Anything with a number and a date goes in a CSV. Anything that is an item goes in a list. Anything that
is a paragraph goes in a note. A row that needs a sentence to make sense is two things: a short row,
and a note.

## Writing

**Append with `bash`. Never with `write`** — `write` replaces the whole file, so one call with stale
content silently erases everything kept before it.

```
echo "- The Left Hand of Darkness" >> lists/books-to-read.md
printf '\n## %s\n\nPrefers oat milk; no sugar.\n' "$(date +%F)" >> notes/preferences.md
```

Rules that keep the files readable a year from now:

- **No commas in a CSV field.** The `note` column is a few words. If it wants a comma, it is a note in
  `notes/`, and the row can say `see notes/2026-09-13-gp.md`.
- **Leave `unit` empty rather than guessing.** `,,` is honest; a wrong unit is a wrong reading.
- **Create a stream, list, or note by creating its file** — a CSV with its header row — then add it to
  `INDEX.md` in the same turn. A file nobody declared is a file nobody can interpret, or find.
- **Look before adding to a list.** `grep -i` it first, so the same item is not kept twice.
- **Correct with `edit`, not by rewriting.** A typo in one row is one exact replacement; an item done
  or no longer wanted is one line removed. Do not quietly delete an observation that turned out to be
  inconvenient; append the corrected one and say in the `note` what it supersedes.

## Reading back

Read a file with a command, not by loading it into context. The file outlives the context window; a
`bash` pipeline does not care how long it has grown.

| Question | Command |
| --- | --- |
| everything in a stream | `cat log/weight.csv` |
| the last ten | `tail -n 10 log/weight.csv` |
| one metric only | `awk -F, '$2=="systolic"' log/blood-pressure.csv` |
| a date range | `awk -F, '$1>="2026-03-01" && $1<="2026-09-01"' log/weight.csv` |
| the trend | `awk -F, 'NR>1{s+=$3;n++} END{print s/n}' log/weight.csv` |
| a list | `cat lists/books-to-read.md` |
| anything mentioning a word | `grep -ri thyroid log lists notes` |

Answer from the file, every time. Do not answer a question about what was kept from what you remember
of the conversation — that is the reading that is most likely to be wrong, and the file is right there.
A question about "this week" or "since Monday" needs `clock` too, to know which days those are.

## Finding the record again

The record says what it is. `INDEX.md` at the top of the working directory names every stream, list,
and note, so `cat INDEX.md` is the way back into it — one call, never out of date, and unaffected by how
much of this conversation has been folded away.

Nothing about the record is carried anywhere else. There is no store that follows you between
conversations: what one conversation holds, another reaches only by `session_search`, and only by
asking for it.

## The one limit to say out loud

These files live in **this session's working directory**. Another conversation cannot see them; it has
its own. So a record belongs to one long-running conversation, and starting a second one for the same
subject splits it in two — the second one will not find the first by accident, because nothing crosses
on its own.

Say this the first time you create a file here, so the operator is choosing it rather than discovering
it later. If a record for this subject might already exist somewhere else, `session_search` with
`session:"all"` is how to find out before starting a second one.
