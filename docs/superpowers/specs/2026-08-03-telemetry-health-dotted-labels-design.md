# Telemetry health dotted-label display names

## Goal

Show the grouped version, harness, and operating-system values as the names of the bars in the Telemetry health
dashboard.

## Problem

The `Active installations by version` and `Active installations by harness and OS` panels group VictoriaLogs stats by
labels whose names contain dots. VictoriaLogs returns those labels correctly, and the existing Prometheus-style
`legendFormat` templates correctly set the dataframe names. Grafana's bar gauge, however, derives each bar title from
the numeric field, whose default name is `installations`, rather than from the dataframe name.

The affected labels are `service.version` and `os.type`. The plain `agent` label shares the harness and operating-system
panel with `os.type`, so that panel must use one display-name expression for both labels.

## Design

Keep the LogsQL queries and the existing VictoriaLogs datasource unchanged. Set Grafana's field display name on each
affected panel:

- `Active installations by version`: `${__field.labels["service.version"]}`
- `Active installations by harness and OS`: `${__field.labels.agent} · ${__field.labels["os.type"]}`

Preserve the existing target-level `legendFormat` values so Query Inspector and other dataframe-name consumers retain
their current names:

- `Active installations by version`: `{{service.version}}`
- `Active installations by harness and OS`: `{{agent}} · {{os.type}}`

Grafana's bracket notation addresses labels whose names contain dots without changing the labels returned by
VictoriaLogs.

## Verification

Add a dashboard contract that requires each affected panel to have exactly one target and the following exact pairs of
`.fieldConfig.defaults.displayName` and `.targets[0].legendFormat`:

- `${__field.labels["service.version"]}` and `{{service.version}}`
- `${__field.labels.agent} · ${__field.labels["os.type"]}` and `{{agent}} · {{os.type}}`

Run the contract before the dashboard change to prove that it catches the existing rendering configuration, then run
it again after the change.

Provision the changed dashboard in the existing local Grafana instance without replacing its configured remote
VictoriaLogs datasource. Query the datasource through Grafana to confirm that the version, harness, and operating-system
labels are present in `schema.fields[].labels` on the numeric field. Inspect the rendered bar gauges and verify these
representative results:

- a version bar is named `v1.2.0`;
- a harness and operating-system bar is named `codex · linux`;
- a missing grouped value is rendered as `Unknown`;
- no bar name starts with `installations`.

Before publishing the branch, review all PR issue comments, reviews, and inline review threads. Apply only feedback that
is relevant and technically correct for the PR, rerun the dashboard and backend checks, and push the verified commit to
the existing PR branch.
