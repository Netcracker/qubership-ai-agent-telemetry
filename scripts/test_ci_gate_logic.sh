#!/usr/bin/env bash

set -euo pipefail

assert_case() {
  local expected=$1
  local description=$2
  local changes_result=$3
  local run_tests=$4
  local job_results=$5

  set +e
  CHANGES_RESULT=$changes_result RUN_TESTS=$run_tests JOB_RESULTS=$job_results evaluate_gate
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
  assert_case 0 'relevant jobs succeeded' success true 'success success'
  assert_case 0 'manual dispatch requires lifecycle jobs to succeed' success true 'success success'
  assert_case 0 'irrelevant jobs were skipped' success false 'skipped skipped'
  assert_case 1 'change detection failed' failure true 'success success'
  assert_case 1 'relevant job was skipped' success true 'success skipped'
  assert_case 1 'relevant job was cancelled' success true 'success cancelled'
  assert_case 1 'irrelevant job unexpectedly ran' success false 'success skipped'
  assert_case 1 'result count is incomplete' success true 'success'
fi
