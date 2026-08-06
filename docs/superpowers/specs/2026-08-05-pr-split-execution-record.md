# PR split execution record

This record captures the reproducible evidence used to split the remote Codex
metrics work from backend maintenance.

## Protected sources

```text
recovery_tag backup/remote-codex-metrics-pre-rewrite-20260805
recovery_target cc2e9995de06cadc331ae8f65935e2d7d18bed22
mixed_source_tag backup/remote-codex-metrics-mixed-source-20260805
mixed_source_target de75b951e6829bc948dd81d4fa7ca53ff696d1bd
```

`git tag -v` reported a good signature from key
`FDF775D49AE53620CC33B7DA418AB3A8A0429747` for both tags.

## Clean metrics baseline

```text
clean_metrics_head cbd0bcb0d0a81be29dbdc1626d9aa2fe0f29f345
maintenance_parent cbd0bcb0d0a81be29dbdc1626d9aa2fe0f29f345
expected_metrics_tree 7cd05b88755bc0671937c8238ee41a6b33421281
actual_clean_metrics_tree 7cd05b88755bc0671937c8238ee41a6b33421281
frozen_patch_sha256 c43c92fee1b95ba8d1847659270493b2b393f505fda1c73f1eae3c7da3bd6d27
```

Before branch creation, both recorded metrics refs resolved to the clean metrics
head.

## Protected metrics blobs

```text
path .github/workflows/telemetry-backend-tests.yaml
expected_blob b774c5dee4220c965027a5551b72aac55547d9c6
actual_blob b774c5dee4220c965027a5551b72aac55547d9c6

path telemetry-backend/.env.example
expected_blob ca252138a3b1a3032aaa9dd36bac6e4275f4e579
actual_blob ca252138a3b1a3032aaa9dd36bac6e4275f4e579

path telemetry-backend/README.md
expected_blob 5f36e7cad92dc33c388f77ba8890888a287c78af
actual_blob 5f36e7cad92dc33c388f77ba8890888a287c78af

path telemetry-backend/docker-compose.yml
expected_blob 30a1f7e390a4d383145a337eb9c6e17922a07f6c
actual_blob 30a1f7e390a4d383145a337eb9c6e17922a07f6c

path telemetry-backend/grafana/dashboards/codex-native-metrics.json
expected_blob 3957488dc4cc064b49f0dcbce89d6e56953eadfe
actual_blob 3957488dc4cc064b49f0dcbce89d6e56953eadfe

path telemetry-backend/tests/config-contract.sh
expected_blob 76989b6eaf5ed4aaf11425fd87cf5550a7c8d958
actual_blob 76989b6eaf5ed4aaf11425fd87cf5550a7c8d958

path telemetry-backend/tests/dashboard-contract.sh
expected_blob 601db4a7f66b6c3452e627effd8e72c425bca56f
actual_blob 601db4a7f66b6c3452e627effd8e72c425bca56f

path telemetry-backend/tests/metrics-query-contract.sh
expected_blob 7b90eca7325c3077a4595739ad611a525527de5b
actual_blob 7b90eca7325c3077a4595739ad611a525527de5b

path telemetry-backend/tests/metrics-query-retry-test.sh
expected_blob 40ddc6cde828cbad21f7c6b2ac30512a697c888c
actual_blob 40ddc6cde828cbad21f7c6b2ac30512a697c888c

path telemetry-backend/tests/with-fixture-stack.sh
expected_blob 481770d8c36b7a04b54ebe10d9b84c90bbaaff9d
actual_blob 481770d8c36b7a04b54ebe10d9b84c90bbaaff9d
```

## Metrics manifests

The late metrics diff from
`b15c3a8d6653b70afa12bf63cabd4a282cac2f75` contains exactly these ten paths:

```text
.github/workflows/telemetry-backend-tests.yaml
telemetry-backend/.env.example
telemetry-backend/README.md
telemetry-backend/docker-compose.yml
telemetry-backend/grafana/dashboards/codex-native-metrics.json
telemetry-backend/tests/config-contract.sh
telemetry-backend/tests/dashboard-contract.sh
telemetry-backend/tests/metrics-query-contract.sh
telemetry-backend/tests/metrics-query-retry-test.sh
telemetry-backend/tests/with-fixture-stack.sh
```

The complete metrics diff from merge base
`2000143a0dae2aec549cf08a7a677a48f3245878` contains exactly these 30 paths:

```text
.github/workflows/telemetry-backend-tests.yaml
docs/adr/0007-native-otlp-metrics-privacy-and-capacity.md
docs/superpowers/plans/2026-07-29-remote-codex-metrics.md
docs/superpowers/plans/2026-07-30-dashboard-usability-redesign.md
docs/superpowers/plans/2026-07-30-multi-harness-native-metrics.md
docs/superpowers/plans/2026-07-31-dashboard-freshness-and-onboarding-docs.md
docs/superpowers/plans/2026-08-03-telemetry-health-dotted-labels.md
docs/superpowers/specs/2026-07-30-dashboard-usability-redesign.md
docs/superpowers/specs/2026-07-30-multi-harness-native-metrics-design.md
docs/superpowers/specs/2026-08-03-telemetry-health-dotted-labels-design.md
telemetry-backend/.env.example
telemetry-backend/Caddyfile
telemetry-backend/README.md
telemetry-backend/docker-compose.yml
telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json
telemetry-backend/grafana/dashboards/codex-native-metrics.json
telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
telemetry-backend/grafana/dashboards/telemetry-health.json
telemetry-backend/grafana/provisioning/datasources/victorialogs.yaml
telemetry-backend/native-otlp-onboarding.md
telemetry-backend/otel-collector-config.yaml
telemetry-backend/tests/config-contract.sh
telemetry-backend/tests/dashboard-contract.sh
telemetry-backend/tests/fixtures/otel-events.json
telemetry-backend/tests/fixtures/otel-metrics.json
telemetry-backend/tests/metrics-query-contract.sh
telemetry-backend/tests/metrics-query-retry-test.sh
telemetry-backend/tests/query-contract.sh
telemetry-backend/tests/smoke.sh
telemetry-backend/tests/with-fixture-stack.sh
```

## Maintenance import

The maintenance baseline used a binary diff from the clean metrics head to the
recovery target.

The diff excluded each protected metrics path with a `:(exclude)<path>`
pathspec before `git apply`.

The staged protected-path diff was empty, and the index retained every expected
blob ID listed above.

```text
maintenance_baseline_commit 25f072ba373d2cc8824803dea209bb887cb189b2
maintenance_baseline_tree c42619c36560b3b1965011b1fda2d78aa128c4fe
maintenance_baseline_parent cbd0bcb0d0a81be29dbdc1626d9aa2fe0f29f345
```

`git verify-commit` reported a good signature for the maintenance baseline
commit.
