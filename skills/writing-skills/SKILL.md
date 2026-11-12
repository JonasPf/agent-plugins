---
name: writing-skills
description: Use when a task of several steps has just worked and could be asked for again, or when asked to write, fix, or improve a skill — how to draft one as skills/<name>/ in the working directory, test it, and hand it over to be installed.
default: off
---

A skill is what the next conversation starts from. This one worked the task out; the next one will
know only what you write down. You cannot install a skill yourself. You draft it in your working
directory, and the operator installs it from the conversation's files, which is them deciding they
want it.

## When to offer one

- **A task of several steps just worked and could be asked for again** — especially one that took
  detours, needed a correction from the operator, or hit a trap. Offer in one line at the end of the
  answer: "This worked. Want me to save it as a skill, so the next conversation starts from here?"
  Do not draft one unasked; it costs a turn, and whether it is worth keeping is the operator's call.
- **Asked to fix or improve a skill.** Read it with `skill_read` first. It returns the file exactly
  as stored, front matter included, and names its other files: start from all of it, and write the
  new version whole, under the same name.

## Write down what worked, not what should

Before writing, gather from this conversation: the steps that worked, in order; the commands, the
tools, and the models you called (`call_model`) and which of them did the job; what failed and why;
what the operator corrected; what the input looked like and what the result had to be. That is the
skill. One written from what ought to work sends the next conversation into the detours this one
already took.

## The folder

```
skills/<name>/
  SKILL.md          the procedure, front matter first
  scripts/          code for steps that are the same every time
  references/       long tables and examples, read only when SKILL.md says so
  evals/<case>/     test cases
```

Everything in it is text. A file that is not text refuses the whole skill, so never copy in a
binary, a model, or a sample video: say what the skill needs and how to get it.

## SKILL.md

The front matter is three keys, each on one line, read a line at a time:

```
---
name: voice-over
description: Use when a video needs its narration transcribed, cleaned up, and spoken back — extracting the audio, transcribing with timestamps, rewriting, speaking each part, and putting it on the video.
default: on
---
```

- **name**: lowercase words joined by hyphens, the same as the folder.
- **description**: one line, starting `Use when` (or `Use before`). It is the only thing any
  conversation sees until it reads the skill, so it decides whether the skill is used at all. Name the
  situation the way the operator would ask for it — their words, the kinds of file — then what the
  skill covers. Skills are read too seldom rather than too often, so be concrete and a little eager.
  A YAML block (`|` or `>`) is not read here; the install refuses it.
- **default**: `on`, unless the skill is for rare, deliberate use.

The body, in this order:

1. **What the task is and what done looks like** — the file it produces, the answer it gives.
2. **The steps.** Imperative, in order, with the exact commands, models, voices, and parameters
   that worked. Give the reason for any step that is not obvious: a model follows a reason into a
   case you did not foresee, and follows a bare rule blindly or not at all.
3. **The traps** this conversation hit, each with what goes wrong and what to do instead.

Keep it under about two hundred lines. A long table or a worked example goes in `references/`, and
SKILL.md says when to read it (`skill_read` with its path as `file`). Write nothing you did not see
work: the next conversation will trust a claim like "the script installs ffmpeg itself" and lose a
round finding out it does not.

## Scripts

When a step is the same code every time — extracting audio, joining tracks, converting a format —
put it in `scripts/` and call it from SKILL.md, instead of having each conversation write it again.
A script takes its paths as arguments, prints what it did, and exits non-zero with a sentence on
failure. While you draft, run it from `skills/<name>/scripts/` here. Once installed, `skill_read`
says where the skill's folder is: its scripts run from there (`python3 <folder>/scripts/x.py`), and
cannot be written, so a script writes its output into the working directory, never beside itself.

## Test cases

Write two or three, each the kind of thing the operator would actually type, in the layout
`references/test-cases.md` describes — read it before writing the first. It is the layout
`claude plugin eval` reads, so the operator can run the same cases from a terminal.

## Run them before handing it over

Run each case yourself, following only the draft, as if you had never seen this conversation: read
SKILL.md from your directory, do what it says step by step, then check each grader's statement
against what you produced. Fix what fails and run it again. Then say what you ran and what passed,
and what you could not check. Never report a check you did not make.

A case's input that is not text, a video say, cannot go in the skill: run with the file in this
conversation, and let the case's prompt name the kind of file it needs.

## Hand it over

Tell the operator, briefly:

- where the draft is: `skills/<name>/`;
- how to install it: in Files, tap **install as skill** on the folder. A skill of the same name is
  replaced, after the interface asks;
- what the test runs showed;
- what to try next: the same request in a new conversation, which has never seen this one. That is
  the test that matters.
