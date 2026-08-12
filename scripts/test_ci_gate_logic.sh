#!/usr/bin/env bash

set -euo pipefail

assert_case() {
  local expected=$1
  local description=$2
  shift 2

  set +e
  "$@" >/dev/null 2>&1
  local actual=$?
  set -e

  if [ "$actual" -ne "$expected" ]; then
    echo "$description: expected exit $expected, got $actual" >&2
    exit 1
  fi
}

evaluate_gate() {
  : "${CHANGES_RESULT:?CHANGES_RESULT is required}"
  : "${RUN_TESTS:?RUN_TESTS is required}"
  : "${JOB_RESULTS:?JOB_RESULTS is required}"

  if [ "$CHANGES_RESULT" != 'success' ]; then
    return 1
  fi

  read -r -a results <<<"$JOB_RESULTS"
  if [ "${#results[@]}" -ne 2 ]; then
    return 1
  fi

  case "$RUN_TESTS" in
    true) expected_result=success ;;
    false) expected_result=skipped ;;
    *) return 1 ;;
  esac

  for result in "${results[@]}"; do
    [ "$result" = "$expected_result" ] || return 1
  done
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  assert_case 0 'relevant jobs succeeded' env \
    CHANGES_RESULT=success RUN_TESTS=true JOB_RESULTS='success success' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 0 'manual dispatch requires lifecycle jobs to succeed' env \
    CHANGES_RESULT=success RUN_TESTS=true JOB_RESULTS='success success' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 0 'irrelevant jobs were skipped' env \
    CHANGES_RESULT=success RUN_TESTS=false JOB_RESULTS='skipped skipped' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 1 'change detection failed' env \
    CHANGES_RESULT=failure RUN_TESTS=true JOB_RESULTS='success success' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 1 'relevant job was skipped' env \
    CHANGES_RESULT=success RUN_TESTS=true JOB_RESULTS='success skipped' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 1 'relevant job was cancelled' env \
    CHANGES_RESULT=success RUN_TESTS=true JOB_RESULTS='success cancelled' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 1 'irrelevant job unexpectedly ran' env \
    CHANGES_RESULT=success RUN_TESTS=false JOB_RESULTS='success skipped' bash -c 'source "$1"; evaluate_gate' _ "$0"
  assert_case 1 'result count is incomplete' env \
    CHANGES_RESULT=success RUN_TESTS=true JOB_RESULTS='success' bash -c 'source "$1"; evaluate_gate' _ "$0"
fi
