# Test cases

A skill's tests live in its folder, one folder per case under `evals/`, in the layout
`claude plugin eval` reads. The layout is strict: copy these templates.

```
skills/<name>/evals/<case>/
  prompt.md               settings, then the prompt
  case.yaml               the case's name, and what success looks like
  graders/01-<words>.md   one checkable statement per file
```

**The case's name** is its folder: lowercase letters, digits, and hyphens, starting with a letter,
saying in a few words what the case checks (`short-narrated-clip`). Not `results`, `mocks`, `files`,
`true`, `false`, or `null`.

**prompt.md** is a settings block copied as it is, then the prompt exactly as the operator would type
it. The block holds no free text: it ends at the first `---` it meets.

```
---
tags: [voice-over]
max_turns: 30
timeout_seconds: 900
allowed_tools: [Bash, Read, Write, Edit]
---

Make a voiceover for clip.mov: transcribe it, tidy the narration, and speak it back over the video.
Input files: clip.mov
```

**case.yaml** — the version quoted, the name the same as the folder, and the outcome indented under
the `|`:

```
schema_version: "1.1"
name: short-narrated-clip
expected_outcome: |
  voiceover.mp4 is clip.mov with the narration replaced by a synthesized voice reading a cleaned-up
  version of what was said, and it runs as long as the original.
```

**graders/01-<words>.md** — the same block on every one, then one statement about the result that
can be checked true or false. Number them so they keep their order.

```
---
type: llm
focus: trace
weight: 1
---

voiceover.mp4 runs as long as clip.mov, to within a second
```

Good statements are about the result, not about where you saved it, and can be checked by looking:
"the transcript has a timestamp on every segment", "no line of the narration repeats the original's
filler words". A case needs at least one.

**Running them elsewhere.** `claude plugin eval` runs each case in Claude Code, with and without the
skill. Claude Code has no `call_model` and none of this agent's tools, so a case that depends on them
only runs here; the format still keeps the cases readable and portable.
