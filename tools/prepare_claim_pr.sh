#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  prepare_claim_pr.sh --lane performance|libgowebrtc-parity|code-quality --surface <surface> --tag <tag> --hypothesis <text> [options]

Options:
  --lane <lane>              Top-level coordination lane.
  --surface <surface>        Editable surface being claimed.
  --tag <tag>                Branch suffix; branch becomes autoresearch/<tag>.
  --owner <name>             Claim owner. Defaults to git user.name or whoami.
  --hypothesis <text>        Current experiment hypothesis.
  --blocked-by <text>        Optional blocker text.
  --queue-dependency <text>  Optional queue dependency text.
  --title <text>             Optional PR title. Defaults to "workflow: <tag>".
  --body-file <path>         Optional body file path.
  --base <branch>            Base branch for draft PR creation. Default: master.
  --push                     Push the branch to origin.
  --create-draft             Open or refresh a draft PR with gh after pushing.
  --dry-run                  Print commands without executing them.
  -h, --help                 Show this help.
EOF
}

die() {
  echo "prepare_claim_pr: $*" >&2
  exit 1
}

run() {
  echo "+ $*"
  if [[ "$DRY_RUN" -eq 0 ]]; then
    "$@"
  fi
}

ensure_repo() {
  [[ -f "program.md" ]] || die "program.md not found; run from the gopus repo root"
  [[ -f "AGENTS.md" ]] || die "AGENTS.md not found; run from the gopus repo root"
  [[ -f ".github/pull_request_template.md" ]] || die ".github/pull_request_template.md not found"
}

default_owner() {
  local owner
  owner="$(git config --get user.name || true)"
  if [[ -n "$owner" ]]; then
    printf "%s\n" "$owner"
    return 0
  fi
  whoami
}

default_body_file() {
  local branch="$1"
  printf "%s/%s-claim-pr.md\n" "$(git rev-parse --git-dir)" "${branch//\//-}"
}

base_ref() {
  if git show-ref --verify --quiet "refs/remotes/origin/$BASE"; then
    printf "origin/%s\n" "$BASE"
    return 0
  fi
  if git show-ref --verify --quiet "refs/heads/$BASE"; then
    printf "%s\n" "$BASE"
    return 0
  fi
  die "base branch '$BASE' was not found locally or at origin/$BASE"
}

default_claim_commit_message() {
  printf "%s\n" "chore: open workflow claim"
}

primary_target_for_lane() {
  case "$1" in
    performance) printf "%s\n" "bench-guard plus performance ledger or bench-encode evidence" ;;
    libgowebrtc-parity) printf "%s\n" "explicit target tests, fixtures, or side-by-side parity evidence" ;;
    code-quality) printf "%s\n" "targeted tests plus qualitative maintainability evidence" ;;
    *) die "unsupported lane: $1" ;;
  esac
}

write_body() {
  local path="$1"
  cat >"$path" <<EOF
## Claim

Lane: $LANE
Editable surface: $SURFACE
Owner: $OWNER
Hypothesis: $HYPOTHESIS
Blocked by: $BLOCKED_BY
Queue dependency: $QUEUE_DEPENDENCY

## Overlap Check

- [ ] I checked the active draft PR queue for the same lane and editable surface.
- [ ] No other editable PR currently owns this pair, or this PR is read-only scout work.
- [ ] This draft PR was opened before the first editable code change on this branch.
- [ ] This slice stays within one editable surface plus, if needed, one narrow supporting helper.

## Progress

Current blocker:
Latest result row or qualitative evidence:
Attempts, failures, and results:
Next action:

## Evidence

Primary judge or target: $(primary_target_for_lane "$LANE")
Commands run:
Risk and rollback notes:

## Merge Readiness

- [ ] Rebasing onto the current queue head is done.
- [ ] The lane-specific evidence was rerun after the last rebase.
- [ ] \`make bench-guard\` passed if this change touches a hot path.
- [ ] This PR is ready to merge on its own, not as part of a batch.
EOF
}

open_pr_summary() {
  local branch="$1"
  command -v gh >/dev/null 2>&1 || return 0
  gh auth status >/dev/null 2>&1 || return 0
  gh pr list --head "$branch" --state open --limit 1 --json number,isDraft,title,url --jq 'if length == 0 then "" else .[0] | "\(.number)\t\(.isDraft)\t\(.url)\t\(.title)" end'
}

ensure_claim_commit() {
  local base_ref_name="$1"
  local branch="$2"
  local ahead_count

  ahead_count="$(git rev-list --count "${base_ref_name}..HEAD")"
  if [[ "$ahead_count" != "0" ]]; then
    return 0
  fi

  if [[ -n "$(open_pr_summary "$branch")" ]]; then
    return 0
  fi

  run git commit --allow-empty -m "$(default_claim_commit_message)"
}

LANE=""
SURFACE=""
TAG=""
OWNER=""
HYPOTHESIS=""
BLOCKED_BY=""
QUEUE_DEPENDENCY=""
TITLE=""
BODY_FILE=""
BASE="master"
PUSH=0
CREATE_DRAFT=0
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --lane) LANE="${2:-}"; shift 2 ;;
    --surface) SURFACE="${2:-}"; shift 2 ;;
    --tag) TAG="${2:-}"; shift 2 ;;
    --owner) OWNER="${2:-}"; shift 2 ;;
    --hypothesis) HYPOTHESIS="${2:-}"; shift 2 ;;
    --blocked-by) BLOCKED_BY="${2:-}"; shift 2 ;;
    --queue-dependency) QUEUE_DEPENDENCY="${2:-}"; shift 2 ;;
    --title) TITLE="${2:-}"; shift 2 ;;
    --body-file) BODY_FILE="${2:-}"; shift 2 ;;
    --base) BASE="${2:-}"; shift 2 ;;
    --push) PUSH=1; shift ;;
    --create-draft) CREATE_DRAFT=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "unknown argument: $1" ;;
  esac
done

case "$LANE" in
  performance|libgowebrtc-parity|code-quality) ;;
  *) usage; die "--lane is required" ;;
esac

[[ -n "$SURFACE" ]] || die "--surface is required"
[[ -n "$TAG" ]] || die "--tag is required"
[[ -n "$HYPOTHESIS" ]] || die "--hypothesis is required"

ensure_repo

OWNER="${OWNER:-$(default_owner)}"
TITLE="${TITLE:-workflow: $TAG}"
branch="autoresearch/$TAG"
BODY_FILE="${BODY_FILE:-$(default_body_file "$branch")}"

if git show-ref --verify --quiet "refs/heads/$branch"; then
  run git switch "$branch"
else
  run git switch -c "$branch"
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "+ write body file $BODY_FILE"
else
  mkdir -p "$(dirname "$BODY_FILE")"
  write_body "$BODY_FILE"
fi

echo "prepare_claim_pr: branch=$branch"
echo "prepare_claim_pr: body_file=$BODY_FILE"
echo "prepare_claim_pr: title=$TITLE"

if [[ "$PUSH" -eq 1 || "$CREATE_DRAFT" -eq 1 ]]; then
  ensure_claim_commit "$(base_ref)" "$branch"
  run git push -u origin "$branch"
fi

if [[ "$CREATE_DRAFT" -eq 1 ]]; then
  command -v gh >/dev/null 2>&1 || die "gh CLI is required for --create-draft"
  gh auth status >/dev/null 2>&1 || die "gh auth is required for --create-draft"
  pr_summary="$(open_pr_summary "$branch")"
  if [[ -n "$pr_summary" ]]; then
    IFS=$'\t' read -r pr_number _ _ _ <<<"$pr_summary"
    run gh pr edit "$pr_number" --title "$TITLE" --body-file "$BODY_FILE"
  else
    run gh pr create --draft --base "$BASE" --head "$branch" --title "$TITLE" --body-file "$BODY_FILE"
  fi
fi
