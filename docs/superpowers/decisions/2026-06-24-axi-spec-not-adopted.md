# AXI specification not adopted

**Date:** 2026-06-24
**Status:** accepted

AXI ([kunchenguid/axi](https://github.com/kunchenguid/axi)) targets work CLIs — tools
an agent calls many times per task, iterating through collections, drilling into details,
and acting on what it finds. TOON format, minimal schemas, truncation, pagination, and
contextual disclosure all assume that cycle.

Our CLI has two call sites, neither of which fits:

- **`ingest` / `flush`** run from hooks, fire-and-forget. The agent never reads their
  output. AXI is entirely irrelevant here.
- **Provisioning** (`status` → `provision` → `selftest`) is a one-shot diagnostic
  workflow. A few AXI ideas apply at the margin (structured errors, idempotent mutations),
  but we already implement them. The one AXI recommendation that conflicts — making token
  input non-interactive — is wrong for us: the model must not see the token, and the
  env-file import path already gives the model a non-interactive alternative.
