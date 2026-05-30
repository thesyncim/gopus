#!/bin/bash
# Enumerate ALL test failures across tags + the oracle tag on current main.
set -u
cd /Users/thesyncim/GolandProjects/gopus
export GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1
export PATH=/usr/local/go/bin:$PATH

run() {
  local label="$1"; shift
  echo "##### $label #####"
  go test "$@" ./... -count=1 2>&1 | grep -E "^--- FAIL|^FAIL|panic:" | grep -v "^ok" | head -40
  echo "----- end $label -----"
}

run "default"
run "purego" -tags purego
run "gopus_dred" -tags gopus_dred
run "gopus_qext" -tags gopus_qext
run "gopus_extra_controls" -tags gopus_extra_controls
run "gopus_libopus_oracle" -tags gopus_libopus_oracle
echo "##### SCAN COMPLETE #####"
