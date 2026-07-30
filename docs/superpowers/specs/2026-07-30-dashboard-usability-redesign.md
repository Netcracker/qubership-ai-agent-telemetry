# Dashboard usability redesign

## Goal

Make every telemetry dashboard answer a specific operational question without requiring the reader to infer whether a
value is a lifetime total, a rate, or a count for the selected time range.

The redesign covers these provisioned dashboards:

- Native agent metrics overview
- Codex native metrics
- Adoption overview
- Telemetry health

## Design principles

- Prefer time series over header stats when the trend is more useful than the latest aggregate.
- Name the aggregation window in the panel title and description.
- Use fixed one-hour windows and a minimum one-hour query interval for hourly activity panels.
- Display token counts as whole locale-formatted numbers with digit grouping.
- Use matrices for two-dimensional token breakdowns.
- Preserve zero as a valid measurement and distinguish it from missing telemetry.
- Do not present an inferred value as exact when the available signals cannot support that conclusion.
- Keep each dashboard focused on its primary audience and question.

## Measurement definitions

### Hourly activity

Each hourly point uses `increase(<counter>[1h])`. The query step is at least one hour. A point therefore represents the
activity observed during the trailing hour, not an instantaneous per-second rate.

### Inactive installation

An installation is inactive for a threshold when its `machine.id` appeared during the previous 30 days but did not
appear during the threshold window. The dashboard reports separate 24-hour and 48-hour cohorts.

The stale installation table uses the 24-hour definition and shows the last observed timestamp, harness, and agent
version.

### Target-version adoption

The health dashboard has a visible `target_version` variable with a default of `v1.2.0`. An installation is not on the
target version when it appeared during the previous 30 days but never appeared with the exact selected version during
that period.

Development and locally modified versions do not match a stable target version unless the operator selects that exact
value.

### Native OTLP coverage

Exact installation-level OTLP coverage is not measurable. Hook telemetry has `machine.id`, while the Codex and Claude
native OTLP metrics do not expose a shared installation identifier.

The health dashboard must state this limitation. It may show native-metric freshness by harness, but it must not label
freshness or the presence of a harness-wide signal as the number or percentage of configured installations.

## Native agent metrics overview

The dashboard answers: "Which native agent signals are available, and how much activity did each harness produce?"

### Panels

- Keep Signal availability as a descriptive support matrix.
- Keep Metrics freshness and clarify that it reports the last received native signal for each harness.
- Replace Top-level sessions started with Top-level sessions per hour.
- Replace Token usage over time with Tokens processed per hour.
- Replace Tokens by model and type with a matrix:
  - rows: `Harness · Model`;
  - columns: normalized token types;
  - values: token counts for the selected dashboard range.
- Replace Observed client versions with a compact table that contains only Harness and Version.

Codex and Claude token-type names remain source-native in this PR. The dashboard does not equate semantically different
cache counters.

## Codex native metrics

The dashboard answers: "How is Codex being used, and are its tools, model calls, or latencies degrading?"

### Header activity

Replace the six aggregate stats and the redundant Tool and MCP activity panel with:

- Sessions and turns per hour;
- Tool and MCP calls per hour;
- Tool failure ratio per hour;
- Tokens processed per hour.

The failure ratio uses a one-hour numerator and denominator and renders zero when calls exist without failures.

### Breakdowns and latency

- Keep Top tools with a bounded result set.
- Keep MCP servers and outcomes with a bounded result set.
- Replace Tokens by model and type with a `Model × Token type` matrix.
- Show p50 and p95 for turn, tool, and API latency instead of only an average.
- Keep Skill injections with a bounded result set.

## Adoption overview

The dashboard answers: "How broadly is hook telemetry adopted, and what work is happening across installations and
repositories?"

Replace the three header stats with daily time series:

- Active installations per day;
- Active repositories per day;
- Sessions observed per day.

Keep the cumulative onboarding graph because it answers a different question from daily activity.

Preserve the existing matrix, graph, and table representations of the same skill and MCP data. This duplication is
intentional: the matrix supports comparison, the graph shows distribution, and the table supports exact lookup.

Rename Activity per machine to Activity per installation. Retain machine identifiers only in diagnostic tables where an
operator needs to identify an installation.

## Telemetry health

The dashboard answers: "Which installations or telemetry paths need operator attention?"

Remove these legacy data-quality panels:

- Event ID coverage;
- Machine ID coverage;
- Duplicate delivery rate;
- MCP duration coverage;
- Data quality by version.

Add:

- a short explanation of the installation-level OTLP coverage limitation;
- Machines not seen on target version during the previous 30 days;
- Inactive installations for more than 24 hours;
- Inactive installations for more than 48 hours;
- Native metrics freshness by supported harness;
- a stale installations table with machine ID, last seen, harness, and observed version;
- active installation distribution by agent version;
- active installation distribution by harness and operating system.

The dashboard retains the 30-day lookback for adoption and stale-machine cohort calculations even when the global
dashboard range is shorter. Panel descriptions must disclose this behavior.

## Presentation rules

- Use locale-formatted whole numbers for token counts and table cells.
- Use percent units only for ratios.
- Use duration units that match the metric source.
- Avoid `K`, `M`, or scientific notation where an exact count is the primary information.
- Give every non-obvious query a description that states its aggregation window and population.
- Bound categorical panels to the top 10 or top 15 entries so labels remain readable.
- Use No data for absent telemetry and numeric zero only when the query establishes a real zero.

## Compatibility and data sources

The provisioned dashboard JSON continues to reference the repository's `victorialogs` and `victoriametrics` UIDs.

For local validation only, the import procedure remaps `victoriametrics` to `victoriametrics-local` in memory. It must
not modify, recreate, or delete any Grafana data source.

Queries must remain compatible with the deployed VictoriaLogs version. The stale-installation queries use the tested
`options(ignore_global_time_filter=true)`, `_time`, `in()`, `uniq`, and `max()` constructs.

## Verification

Contract tests must verify:

- one-hour windows and minimum query intervals for hourly panels;
- matrix transformations and exact-count number formatting;
- the absence of a Time column from Observed client versions;
- the presence of the target-version variable and the 24-hour and 48-hour stale definitions;
- removal of the legacy health panels;
- retention of all three adoption representations for skills and MCPs;
- unchanged provisioned data-source UIDs.

After automated checks pass, import all four dashboards into `http://localhost:13000` without changing data sources.
Use browser developer tools to verify populated, zero, and no-data states against the existing real data.
