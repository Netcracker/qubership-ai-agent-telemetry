# Repository agent instructions

## Non-obvious invariants

- Write every committed file in English, including documentation, code comments, commit messages, and identifiers.
- Treat `README.md` and `docs/` as maintained documentation. Files under `docs/superpowers/` are historical snapshots;
  never update them to describe later changes.
- For a design choice that materially changes scope or behavior, ask the user. Present the recommended option first and
  expect it to be challenged.
- Never run `git clean -xdf`: it deletes the ignored, machine-specific root `apm.yml` and unrelated untracked work.
  Preview with `git clean -xdn`, then remove only the intended generated paths.

## Delivery workflow

- Before starting work, committing, pushing, or updating a pull request, run `git fetch origin` and then
  `git merge-base --is-ancestor origin/main HEAD`. Fast-forward `main` or rebase another branch when the check fails.
  If another worktree or user coordination prevents a safe update, stop and ask the user.
- Send every change, including documentation, through a ready-for-review pull request, not a draft. Merge only after
  one approval, all review threads are resolved, and the required checks pass for the current head.
- Keep the pull-request description aligned with the current scope and verification steps after each scope change.
- After each push, inspect checks for the exact head SHA. Report pending, failed, or cancelled checks separately and do
  not describe CI as complete until every expected check is terminal.
- Use Conventional Commits; the pull-request workflows enforce the convention.
- Release only from `main` through the `Release` workflow with a `vMAJOR.MINOR.PATCH` input. The workflow creates the
  tag; never push release tags manually.

## Validation

- Before each push, derive the validation set from `git diff --name-only origin/main...HEAD`, not from the task's
  apparent subsystem. Match every changed path against the workflow trigger filters and internal `paths-filter` rules,
  then run every locally available command from each activated workflow. Record platform-only checks as unavailable.
- For Go changes, run `make test` and `go vet ./...`; `make test` includes the race detector.
- The root `Makefile` does not cover backend or installer behavior. For those changes, follow the commands in
  `.github/workflows/telemetry-backend-tests.yaml`, `.github/workflows/installer-tests.yaml`, and
  `.github/workflows/bootstrap-tests.yaml`.
