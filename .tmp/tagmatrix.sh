#!/bin/bash
# Test-and-adjust-all-tags matrix: builds + full ./... parity sweeps per tag.
set -u
cd /Users/thesyncim/GolandProjects/gopus
export GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1
export PATH=/usr/local/go/bin:$PATH

TAGSETS=(
  ""                                            # default
  "purego"
  "gopus_dred"
  "gopus_extra_controls"
  "gopus_qext"
  "gopus_dred gopus_extra_controls gopus_qext"  # all optional
)

echo "###### BUILD MATRIX ######"
for t in "${TAGSETS[@]}"; do
  if [ -z "$t" ]; then
    out=$(go build ./... 2>&1); rc=$?
  else
    out=$(go build -tags "$t" ./... 2>&1); rc=$?
  fi
  echo "BUILD [${t:-default}] rc=$rc"
  [ $rc -ne 0 ] && echo "$out" | head -10
done

echo ""
echo "###### TEST MATRIX (full ./... parity) ######"
for t in "${TAGSETS[@]}"; do
  echo "===== TEST [${t:-default}] ====="
  if [ -z "$t" ]; then
    go test ./... -count=1 2>&1 | grep -E "FAIL|panic:|build failed|cannot" | head -40
  else
    go test -tags "$t" ./... -count=1 2>&1 | grep -E "FAIL|panic:|build failed|cannot" | head -40
  fi
  echo "----- done [${t:-default}] -----"
done
echo "###### MATRIX COMPLETE ######"
