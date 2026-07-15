# Java 21 prerequisite for global Git hooks

## Context

The Qubership developer installer installs `exadmin/pre-commit-global` as its `git-hooks` component. Those hooks run
CyberFerret CLI 1.3.0. The published CLI JAR uses Java class-file version 65, the CyberFerret Maven build targets Java
21, and CyberFerret CI builds with Java 21. A Docker verification with Temurin 21.0.11 loaded the released CLI and
reached its environment-variable validation without a CyberFerret password or dictionary access.

The existing POSIX and PowerShell installers check only whether a `java` command exists. A Java installation older
than 21 therefore passes installation but fails when CyberFerret runs during a later commit.

## Scope

Change the POSIX and PowerShell developer installers in pull request 14 so the `git-hooks` component requires Java 21
or newer. Update their black-box tests and the developer-installer README. Update the draft documentation pull request
for `exadmin/pre-commit-global` to state the same runtime requirement.

A CyberFerret documentation pull request is a recommendation only and is not part of this implementation.

## Version detection

When `git-hooks` is selected, each installer must:

1. Confirm that `java` is available on `PATH`.
2. Run `java -XshowSettings:properties -version` and capture both standard output and standard error.
3. Read the `java.specification.version` property.
4. Normalize a legacy `1.x` value to `x`; otherwise, use the leading integer as the Java major version.
5. Accept a recognized major version greater than or equal to 21.

The standardized system property is preferred over parsing the vendor-specific `java -version` banner. The minimum
version must be stored once in each standalone installer so messages and comparisons cannot drift within that script.

## Failure behavior

Treat each of the following as an unmet prerequisite:

- `java` is absent;
- the Java command fails;
- `java.specification.version` is missing or cannot be parsed;
- the detected Java major version is lower than 21.

For an older version, report both the detected major version and the Java 21-or-newer requirement. For a failed or
unrecognized result, explain that the installer could not determine the version and tell the user to install Java 21
or newer.

Preserve the existing prerequisite interaction:

- non-interactive mode exits with status 1 before installing any component;
- interactive mode asks once whether the user installed or updated the prerequisites, then repeats the complete check;
- a negative answer or a failed second check exits with status 1;
- no Git or Java prerequisite check runs when `git-hooks` is not selected.

The prompt should refer to installing or updating prerequisites because an existing Java installation may be too old.

## Tests

Extend both black-box suites with controllable fake Java commands. Cover these cases:

- Java 20 is rejected before component side effects;
- Java 21 is accepted;
- a version newer than 21 is accepted;
- missing or malformed `java.specification.version` output is rejected;
- prerequisite checks remain skipped when `git-hooks` is not selected;
- the existing missing-Git and missing-Java behavior remains unchanged.

The fake commands must respond to `-XshowSettings:properties -version` in the same channel used by real Java. The CI
tests do not need Docker. The isolated Docker run is manual evidence that the released CyberFerret CLI starts on the
minimum supported Java version.

## Documentation

The developer-installer README must state that Git and Java 21 or newer are required only for `git-hooks`. It must also
describe the one-time install-or-update prompt and recheck.

The draft `exadmin/pre-commit-global` README change should replace the Oracle JDK 25 testing statement with a
vendor-neutral Java 21-or-newer runtime requirement. It should not add build instructions for CyberFerret.
