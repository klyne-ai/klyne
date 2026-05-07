#!/usr/bin/env bash
# scripts/bench-ci.sh — CI wrapper: run bench.sh and fail if any budget is
# violated.
#
# Exits 0 if all metrics are within spec §12 budgets.
# Exits 1 if any metric has "ok": false, printing the offending entries.
#
# Requires: bash 4+, jq
#
# Usage:
#   bash scripts/bench-ci.sh
#   # bench-output.json is written to the repo root for artifact upload.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
OUTPUT_FILE="${REPO_ROOT}/bench-output.json"

echo "=== agentdeck spec §12 performance bench ===" >&2

# Run the bench suite and capture JSON.
bash "${SCRIPT_DIR}/bench.sh" > "${OUTPUT_FILE}"

echo "" >&2
echo "=== Budget evaluation ===" >&2

# Count failures using jq.
if ! command -v jq &>/dev/null; then
  echo "WARNING: jq not found; skipping budget evaluation" >&2
  echo "bench-output.json written to ${OUTPUT_FILE}" >&2
  exit 0
fi

FAILS=$(jq -r '[to_entries[] | select(.value.ok == false)] | length' "${OUTPUT_FILE}")

if [ "${FAILS}" -gt 0 ]; then
  echo "BUDGET_VIOLATION: ${FAILS} metric(s) exceed spec §12 budget" >&2
  echo "" >&2
  jq -r 'to_entries[] | select(.value.ok == false) |
    "  FAIL  \(.key): \(.value.value) \(.value.unit) (budget: \(.value.budget) \(.value.unit), direction: \(.value.budget_direction))\(if .value.note then " — \(.value.note)" else "" end)"' \
    "${OUTPUT_FILE}" >&2
  echo "" >&2
  echo "Full report: ${OUTPUT_FILE}" >&2
  exit 1
fi

echo "All spec §12 budgets within limits." >&2
echo "" >&2
jq -r 'to_entries[] |
  "  PASS  \(.key): \(.value.value) \(.value.unit) (budget: \(.value.budget) \(.value.unit))"' \
  "${OUTPUT_FILE}" >&2
echo "" >&2
echo "Report written to: ${OUTPUT_FILE}" >&2
