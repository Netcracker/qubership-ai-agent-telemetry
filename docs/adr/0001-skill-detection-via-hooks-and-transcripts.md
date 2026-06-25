# Skill-usage detection via native hooks and session transcripts

## Status
Accepted
#### Date
2026-06-17
#### Owner
denifilatoff
#### Participants and approvers
Denis Filatov (@denifilatoff)
#### Related ADRs
None

## Context

The project needs to know when a skill runs inside an AI-agent session. Four agents are in scope: Claude Code,
Codex, Cursor, and OpenCode. No single detection method covers all four — each agent exposes a different set of
hooks, events, and session artifacts.

An early prototype used a **visible marker** (a `[skill-called]` line) printed into the model's response on every
skill activation. A hook then matched that line and reported the event. The marker was the simplest cross-agent
signal: it worked wherever the model could print text and a hook could read the response. However, the approach
had three problems:

1. **Telemetry information leaks into the user's dialog.** The marker is part of the model's visible output. A
   user who sees `[skill-called] skill=deploy-helm source=…` in the middle of a response gets an unexplained
   line that looks like a malfunction. When the skill returns structured data (JSON, YAML), the extra line
   corrupts the payload and a downstream parser may reject the whole response as malformed.
2. **Skill authors must remember to place the marker.** Every skill body needs a "print this line first"
   instruction. Forgetting it — or placing it after a conditional branch — silently drops the event. The
   telemetry contract is invisible at install time and enforced only by convention.
3. **Detection is probabilistic.** The model, not the agent, emits the signal. If the model omits or rewrites
   the marker, the event is lost. This makes the marker an undercount by design — acceptable for rough
   estimates, not for reliable tracking.

Meanwhile, some agents expose a **native skill-activation event** (Claude Code fires a `PreToolUse` hook on the
`Skill` tool; OpenCode emits a `use_skill` tool call), while others record skill-file reads in a **session
transcript** that a post-turn hook can parse. A full comparison of eight candidate approaches is in
[the detection-options research](../superpowers/research/2026-06-detection-options.md).

A separate constraint shaped the delivery model: telemetry must track only repositories that **opted in** by
installing the hooks package, not every repository the developer works in. The agent's own built-in telemetry
(option 1 in the research) is global — it reports from every repository once enabled — and offers no per-project
scope. Repository-scoped hooks, delivered through the APM package manager, make installation the consent boundary:
a repository sends telemetry only after `apm install` adds the hooks package.

## Decision

We will detect skill usage through two complementary signals, delivered as repository-scoped hooks via an APM
package:

1. **Native skill-activation hook** — where the agent emits a skill event, the hook reads the skill name
   directly from the event payload. Used for Claude Code (`PreToolUse` on the `Skill` tool) and OpenCode
   (`use_skill` tool call). This is exact and deterministic.
2. **Session-transcript parsing** — where no native event exists, a post-turn hook reads the session transcript
   and recognizes skill-file reads (`SKILL.md` paths) recorded by the agent. Used for Codex (`Stop` hook,
   JSONL rollout transcript) and Cursor (`afterAgentResponse` hook, JSONL agent transcript). The CLI keeps a
   per-session byte offset and parses only new lines, deduplicating skill names within a turn.

We will **not** use a marker printed into the model's response. The marker was retired from the CLI on
2026-06-17 ([refactor plan](../superpowers/plans/2026-06-17-cli-refactor.md)).

### Justification

Eight approaches were evaluated. The full analysis is in
[the detection-options research](../superpowers/research/2026-06-detection-options.md); the summary follows.

| # | Approach | Verdict |
|---|---|---|
| 1 | Agent's built-in telemetry | Only Claude Code names the skill; indiscriminate — reports from every repository, not just opted-in ones. |
| 2 | Signal emitted from the skill body | Rejected — covert collection the moment the skill is read; pollutes a shared skill. |
| 3 | Route a skill step through an MCP server | Rejected — hard-wires a portable skill to a specific server; plain-text skills cannot be instrumented. |
| 4 | Subscribe to the agent's event stream | OpenCode only; the other three expose no interactive-session interface. |
| 5 | **Native skill-activation hook** | **Chosen where available.** Exact, and the skill body stays untouched. Works on Claude Code and OpenCode. |
| 6 | **Session-transcript parsing** | **Chosen as the fallback.** Reliable on Claude Code and OpenCode, workable on Codex, the only route on Cursor. |
| 7 | Visible marker caught by a hook | Used initially, now retired. Probabilistic undercount; leaks telemetry text into the user's dialog; requires every skill to carry the marker instruction. |
| 8 | Langfuse as the backend | Rejected — accepts only traces; needs a central proxy that breaks subscription auth on Claude Code and Codex. |

The marker (option 7) was rejected for the three reasons in the Context section. The core tradeoff of the chosen
approach is that **transcript parsing searches for raw structural patterns rather than a known, declarative
token**. A marker search is a simple string match against a fixed format; transcript parsing must understand the
agent's JSONL schema and recognize `Read` tool-use entries whose path matches a skill-file pattern. If a harness
changes its transcript format, the parser must be rewritten. We accepted this tradeoff because the transcript is
an agent artifact — its format is tied to the agent version, not to our code — and the alternative (markers) has
worse failure modes: silent undercounts and visible pollution of the user's conversation.

Hooks are repository-scoped by design. The APM package manager installs hooks into a project directory, so only
repositories that carry the `ai-agent-telemetry` package send events. This is the consent boundary: installing the
package is the act of opting in. A developer's personal or non-work repositories are never instrumented unless
they explicitly add the package.

## Consequences

- **Positive.** Skill bodies stay clean — no marker instruction, no telemetry-related text in the output. The
  user never sees telemetry artifacts in the conversation. Detection is exact where a native event exists.
- **Negative.** Transcript parsing is coupled to each agent's JSONL schema. A breaking change in the transcript
  format (field renames, structural changes) requires a parser update in the CLI. The Cursor transcript
  (`state.vscdb`) was already ruled out as unreliable; the current JSONL path depends on Cursor continuing to
  write it.
- **Neutral.** Two detection paths (native event and transcript) coexist. The CLI routes by `--agent` flag, so
  the complexity is in per-agent adapters, not in shared logic.
- **Scope.** Only repositories with the hooks package installed send telemetry. There is no global opt-in, no
  machine-wide instrumentation, and no way to track a repository without its maintainer adding the package.
