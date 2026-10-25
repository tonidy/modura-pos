#!/usr/bin/env bash
# One contract change, end to end (plan M4):
#
#   1. Rename inventory.getStock's `available` to `availableUnits` in the DSL.
#   2. `pnpm generate` refuses: the change breaks clients and the version was not bumped.
#   3. Bump the module, generate again: IR, OpenAPI, SDK, MCP tools and Go interfaces
#      all change in one diff, and a second run changes nothing.
#   4. The stale UI call site and the Go handler stop compiling.
#   5. Fix both, and everything compiles again.
#
# It runs in a throwaway git worktree of HEAD, so your working tree is never touched.
# Set DEMO_REF to use another commit.
# It fails if any step behaves differently, which makes it a check as well as a demo.
# Output lines starting with "### " and "$ " are read by scripts/record-demo.ts.
set -uo pipefail

root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
work="$tmp/modura-pos"
git -C "$root" worktree add --detach --quiet "$work" "${DEMO_REF:-HEAD}"
trap 'git -C "$root" worktree remove --force "$work"; rm -rf "$tmp"' EXIT
cd "$work"
pnpm install --frozen-lockfile --prefer-offline --silent >/dev/null

step() { printf '\n### %s\n' "$1"; }
show() { printf '$ %s\n' "$*"; }
abort() { printf '\nDEMO FAILED: %s\n' "$1" >&2; exit 1; }

# run <expect: ok|fail> <command...>: prints the command and its output, and checks the
# exit status matches the expectation.
run() {
  local expect="$1"; shift
  show "$*"
  "$@" 2>&1 | grep -vE '^\s*$|ELIFECYCLE|ERR_PNPM' | sed -e "s|$work/||g"
  local status=${PIPESTATUS[0]}
  if [[ "$expect" == ok && $status -ne 0 ]]; then abort "expected success: $*"; fi
  if [[ "$expect" == fail && $status -eq 0 ]]; then abort "expected failure: $*"; fi
}

contract=modules/inventory/src/index.ts

step "Rename a field in the inventory contract"
sed -i 's/    available: t.integer/    availableUnits: t.integer/' "$contract"
run ok git --no-pager diff --color=never -U0 "$contract"

step "Generate: the compatibility guard refuses the breaking change"
run fail pnpm -s modura generate

step "Bump inventory to 0.2.0 and generate again"
sed -i "s/version: '0.1.0'/version: '0.2.0'/" "$contract"
run ok pnpm -s generate

step "Every generated surface changed, in one diff"
run ok git --no-pager diff --stat --color=never -- generated packages/pos-sdk/src apps/sales-go/internal/api

step "Deterministic: a second run changes nothing"
run ok pnpm -s modura generate --check
run ok bash apps/sales-go/scripts/generate-check.sh

step "The stale UI call site and the Go handler no longer compile"
run fail pnpm -s --filter @modura/pos-web typecheck
run fail go -C apps/sales-go build ./...

step "Update both call sites"
sed -i 's/result.value.available/result.value.availableUnits/' apps/pos-web/src/lib/useCatalog.ts
sed -i 's/Available: level.Available,/AvailableUnits: level.Available,/' apps/sales-go/internal/httpapi/server.go
run ok git --no-pager diff --color=never -U0 apps/pos-web/src apps/sales-go/internal/httpapi

step "Everything compiles again"
run ok pnpm -s --filter @modura/pos-web typecheck
run ok go -C apps/sales-go build ./...
printf '\nDemo complete: one contract change, every surface updated, stale code caught at compile time.\n'
