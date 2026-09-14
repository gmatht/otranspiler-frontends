#!/bin/sh
# gen.sh — regenerate the profile_example workloads (deterministic).
#
# The point of the corpus is that the *shape* of the input changes the
# two lists that app.py builds, and therefore changes the profile and
# the transpiled C:
#
#   empty.log       0 lines            errors=0    ok=0
#   quiet.log       6 short lines      errors=0    ok=6
#   few_errors.log  4 lines            errors=2    ok=2
#   error_heavy.log 64 lines           errors=60   ok=4
#   mixed.log       40 lines           errors=20   ok=20
#   many.log        300 lines          errors=30   ok=270
#   long_lines.log  8 long lines       errors=3    ok=5
#
# Run:  sh gen.sh          (writes the .log files beside this script)
set -eu
cd "$(dirname "$0")"

: > empty.log

{
  echo "service up"
  echo "cache warm"
  echo "request served"
  echo "tick"
  echo "tock"
  echo "done"
} > quiet.log

{
  echo "ok"
  echo "ERROR disk full"
  echo "ok"
  echo "ERROR disk full again"
} > few_errors.log

{
  i=1
  while [ "$i" -le 64 ]; do
    if [ "$i" -le 60 ]; then
      echo "ERROR worker $i failed"
    else
      echo "worker $i retired"
    fi
    i=$((i + 1))
  done
} > error_heavy.log

{
  i=1
  while [ "$i" -le 40 ]; do
    if [ $((i % 2)) -eq 0 ]; then
      echo "ERROR even line $i"
    else
      echo "odd line $i"
    fi
    i=$((i + 1))
  done
} > mixed.log

{
  i=1
  while [ "$i" -le 300 ]; do
    if [ $((i % 10)) -eq 0 ]; then
      echo "ERROR sampled failure at line $i"
    else
      echo "request line $i served normally"
    fi
    i=$((i + 1))
  done
} > many.log

{
  echo "ERROR this is a deliberately very long error line padded out to be much longer than the rest of the corpus"
  echo "a"
  echo "ERROR another very long error line that keeps going and going and going well past eighty columns for good measure"
  echo "b"
  echo "c"
  echo "ERROR third long error line, padded so the workload mixes long error lines with short ok lines"
  echo "d"
  echo "e"
} > long_lines.log

echo "regenerated workloads in $(pwd)" >&2
