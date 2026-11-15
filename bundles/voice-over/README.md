# voice-over

Puts a synthesized voice-over on a video: it transcribes what the narrator said, places a
script line by line where those words were said, speaks every line in one call, and lays the
speech on the video, its picture untouched. A review page lets the operator hear every line at
its real length, move and change lines, and pick the voice and the language. What each tool
takes and returns is in [CONTRACT.md](CONTRACT.md).

It is one MCP server with its own ffmpeg (`bin/ffmpeg`: FFmpeg, LGPL 2.1 or later, built by
[`ffmpeg/build.sh`](../../ffmpeg/build.sh); `bin/SOURCE.ffmpeg.txt` says how, and each release
carries the source). It needs nothing installed beside it but an
[OpenRouter](https://openrouter.ai) API key, which it uses for transcription and speech, and
which it reads only from its own setting.

## Get it

Each release, `voice-over-v<version>` on the
[releases page](https://github.com/JonasPf/agent-plugins/releases), has one file per platform:
`darwin-arm64`, `linux-amd64`, `linux-arm64`. It is an [MCP Bundle](https://github.com/modelcontextprotocol/mcpb),
a zip. A host that installs MCP Bundles, such as Claude Desktop, takes the file as it is and asks
for the settings below. For any other host, unpack it:

```sh
version=0.0.8 platform=darwin-arm64
mkdir -p ~/.local/share/voice-over && cd ~/.local/share/voice-over
curl -fsSLO "https://github.com/JonasPf/agent-plugins/releases/download/voice-over-v$version/voice-over-$version-$platform.mcpb"
curl -fsSL "https://github.com/JonasPf/agent-plugins/releases/download/voice-over-v$version/sha256sums.txt" | grep "$platform" | shasum -a 256 -c
unzip -o "voice-over-$version-$platform.mcpb"
```

The server is `~/.local/share/voice-over/server`. Fetched with `curl` rather than a browser,
macOS does not hold the programs back as downloads it has not checked.

## Settings

They reach the server as environment variables.

| Variable | |
|---|---|
| `VOICEOVER_OPENROUTER_KEY` | The OpenRouter API key. Without it only `voiceover_frames` works, and the other tools say so. |
| `VOICEOVER_REVIEW` | How the review page is shown: `auto` (the default), `apps` or `browser`. |
| `VOICEOVER_FFMPEG` | Another ffmpeg to use instead of the one in the bundle. |
| `VOICEOVER_OPENROUTER_URL` | Another address for the gateway. |

## Claude Code

```sh
claude mcp add --scope user --transport stdio --env VOICEOVER_OPENROUTER_KEY=sk-or-... voice-over \
  -- ~/.local/share/voice-over/server
```

Claude Code shows no pages, so `voiceover_review` gives a link to the page, served by the plugin
on this machine. Send on the page gives a message to paste back into the chat. A call that
answers nothing for 30 minutes is stopped (`CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT`), which a render
of a video over half an hour can reach: raise it if you voice long recordings.

## Codex

```sh
codex mcp add voice-over --env VOICEOVER_OPENROUTER_KEY=sk-or-... -- ~/.local/share/voice-over/server
```

Then, in `~/.codex/config.toml`, give its calls longer than the default 60 seconds: transcribing
and rendering take minutes.

```toml
[mcp_servers.voice-over]
tool_timeout_sec = 1800
```

The review page is a link, as in Claude Code.

## OpenCode

```sh
opencode mcp add voice-over --global --env VOICEOVER_OPENROUTER_KEY=sk-or-... -- ~/.local/share/voice-over/server
```

or, in its configuration:

```jsonc
{
  "mcp": {
    "servers": {
      "voice-over": {
        "type": "local",
        "command": ["/Users/you/.local/share/voice-over/server"],
        "environment": { "VOICEOVER_OPENROUTER_KEY": "{env:VOICEOVER_OPENROUTER_KEY}" }
      }
    }
  }
}
```

A call may run for twelve hours by default. The review page is a link.

## Using it

Ask for a voice-over on a video by its path. The model transcribes it, writes the script and
checks where each line starts, speaks it, and gives you the review page; `<name>.voiceover.mp4`
appears beside the video, and its working files in `<name>.voiceover/`. Speech costs about two
cents for a ten-minute video in the default voice, `gareth`; the script tool says what a script
will cost before anything is spoken.
