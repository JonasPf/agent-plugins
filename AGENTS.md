# agent-plugins — guide for whoever changes it

Tools for the agent at https://github.com/JonasPf/agent, each an MCP server in
a bundle. Read `README.md` first: it says what a server may assume.

## Before committing

```sh
task check
```

It must pass. Never skip, disable or loosen a test to get there: a failing test
is a signal. Fix the cause.

## Rules

- **Test first.** Write the test that captures the behaviour, watch it fail,
  then make it pass. Every tool has a test that calls it over the protocol
  (`internal/servetest`), not only tests of its functions.
- **A tool's name, description and input schema are its interface.** They live
  in `tools.json`, written by hand, and `manifest.json` repeats the name and
  description; a test holds the two to each other. Conversations name tools,
  so a name never changes. A description changes only with its eval cases
  (`evals/<tool>.json`), which are put to a real model by the agent's eval
  runner. A tool only a page calls (`_meta.ui.visibility: ["app"]`) has
  none: a model is never shown it.
- **A page is an MCP App.** A tool names it under `_meta.ui.resourceUri` in
  `tools.json`, and the bundle hands it to the server as a `serve.Page`; a
  page no tool names, or a name with no page, is refused. A page gets its data
  through tool calls, and has no network and no origin.
- **A failure is a result.** A tool returns an error the model can act on; it
  never ends the server. Say what went wrong and what to do next.
- **No state between calls, no host assumed.** See the README.
- **One bundle, one permission.** A tool that needs something its neighbours
  do not (a key, a path, a longer time) gets its own bundle. What a bundle
  needs from the agent beyond MCPB's own fields goes under
  `_meta["io.github.jonaspf.agent"]`: `timeout_seconds`, `reads`.
- **A behaviour change bumps the bundle's `version`** in its `manifest.json`,
  in the same change.
- **A skill is text a model reads, plus scripts it runs.** Its folder is its
  name. A script is tested on real input (`testenv/`, run by `task check`,
  which needs `python3` and ffmpeg 8). Its `evals/` are in the layout
  `claude plugin eval` reads, and are not released with it.
