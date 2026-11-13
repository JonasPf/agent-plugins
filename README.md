# agent-plugins

Tools and skills for [the agent](https://github.com/JonasPf/agent). The tools are packaged as
[MCP Bundles](https://github.com/modelcontextprotocol/mcpb). Each bundle is one
small MCP server in Go, built as a single program with nothing to install
beside it, so it also runs in any other host that takes MCP bundles or a local
MCP server.

| Bundle | Tools | Needs |
|---|---|---|
| `files` | `read`, `write`, `edit` | nothing |
| `shell` | `bash` | a host that allows a call 15 minutes |
| `clock` | `clock` | nothing |
| `web-fetch` | `web_fetch` | network |
| `web-search` | `web_search` | a [Tavily](https://tavily.com) API key, set in the bundle's settings |
| `web-browse` | `web_browse` | Linux, with chromium at `/usr/bin/chromium` |
| [`voice-over`](bundles/voice-over/README.md) | `voiceover_frames`, `voiceover_transcribe`, `voiceover_script`, `voiceover_render`, `voiceover_review`: [its contract](bundles/voice-over/CONTRACT.md), and [how to install it](bundles/voice-over/README.md) in Claude Code, Codex and OpenCode | an [OpenRouter](https://openrouter.ai) API key, set in the bundle's settings; nothing else: it carries its own ffmpeg (LGPL, built from source by [`ffmpeg/build.sh`](ffmpeg/build.sh), its source in each release); for the review page, a host that shows MCP Apps, or one that keeps the server running for a link on this machine |

A bundle is the unit of permission: tools that need the same things live
together, and a tool that needs more has a bundle of its own.

The skills are [Agent Skills](https://agentskills.io) folders: a `SKILL.md`
and the files beside it.

| Skill | For |
|---|---|
| `researching` | answering from the web with sources |
| `keeping-records` | keeping what the operator asks to remember, as files |
| `writing-skills` | drafting a new skill for the operator to install |
| `video-edit` | cutting and joining videos; needs `ffmpeg` and `python3` |

## What a server may assume

- **The working directory is the workspace.** The file tools reach nothing
  outside the directory the server was started in. Where the host confines the
  server, the operating system enforces that; the tools' own check is the error
  message, not the boundary.
- **Nothing is kept between calls.** The agent starts the server again for
  every call. Anything worth keeping is a file in the working directory.
- **No host is assumed.** A server does not rely on variables only one host
  sets. The one exception is `AGENT_SKILL_DIRS`, which `read` uses when it is
  there and does without when it is not.

## Layout

```
bundles/<bundle>/manifest.json     the MCP Bundle manifest (0.3, server.type "binary")
bundles/<bundle>/tools.json        what a model is shown: name, description, input schema
bundles/<bundle>/*.go              the server and its tests
bundles/<bundle>/evals/<tool>.json eval cases for each tool's description
internal/serve                     turns the two files and the code into an MCP server
internal/servetest                 calls a bundle over the protocol, for tests
internal/workspace, internal/web   what the tools share
cmd/pack                           builds a .mcpb for one platform, or zips a skill
skills/<name>/SKILL.md             a skill, with its scripts, references and eval cases
testenv/                           tests of the skills' scripts, and what installs their ffmpeg
```

## Working on it

```sh
task check                 # format, vet, build, test: the gate before a commit
task pack BUNDLE=clock     # dist/clock-<version>-<os>-<arch>.mcpb for each platform
```

`web-browse`'s browser test needs chromium at `/usr/bin/chromium` and is
skipped where there is none; CI has it and fails without it.

## Releasing a bundle

Change `version` in the bundle's `manifest.json`, merge, then tag the commit
`<bundle>-v<version>` (for example `clock-v0.1.0`) and push the tag. The
release holds one `.mcpb` per platform and a `sha256sums.txt`. A `.mcpb` holds
the manifest, the server, and the tool's eval cases under `evals/`.

Push one tag per push. GitHub starts no workflow for a push that carries more
than three tags.

## Releasing a skill

Tag the commit `skill-<name>-v<version>` (for example
`skill-researching-v0.1.0`) and push the tag. The release holds
`<name>-<version>.zip`, the skill's files with `SKILL.md` at the top and its
eval cases left out, and a `sha256sums.txt`. The version is the tag's; nothing
in the skill repeats it. The agent installs the zip from its Skills screen, or
ships it by naming it in its `plugins.lock`.

## Trying a bundle in the agent

Unpack a Linux `.mcpb` (it is a zip) into `plugins/<bundle>/` in the agent's
state directory and reload its tools. Or install it on the agent's Plugins
screen, from the file or from its address in a release.
