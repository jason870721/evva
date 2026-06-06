#!/usr/bin/env bash
#
# depcheck enforces Veronica global invariant #1 — the multi-agent oracle:
# everything under internal/swarm/** must reach the agent runtime ONLY through
# the public pkg/* surface (agent.New, pkg/event, pkg/tools, pkg/skill, ...),
# never by directly importing internal/agent or any other evva internal package.
# The one sanctioned exception is the public inbox-drainer seam on pkg/agent
# (SPRD-1-12), which is public by design.
#
# IMPORTANT: this checks DIRECT imports, not transitive ones. The public SDK
# packages (pkg/agent, pkg/skill, ...) are thin facades that themselves import
# evva internal/ packages — so a transitive `go list -deps` would always report
# internal/agent the moment swarm legitimately uses pkg/agent, which is exactly
# what the design wants. What must hold is that swarm CODE only WRITES imports
# of pkg/* (+ internal/swarm). That is precisely what examples/full-host (a
# separate module, where internal/* imports are compile errors) proves about
# pkg/*; this is the in-module equivalent guard.
#
# See: docs/veronica/veronica-design-v1.md §1.1, docs/veronica/prd-phase1-swarm.md §5.5.
set -euo pipefail

MODULE="github.com/johnny1110/evva"

# Direct imports of every package under internal/swarm (production code; test
# files' imports are excluded by `go list`). A direct import of an evva
# internal/ package other than internal/swarm itself is a violation.
violations="$(go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/swarm/... \
  | sort -u \
  | grep -E "^${MODULE}/internal/" \
  | grep -vE "^${MODULE}/internal/swarm($|/)" || true)"

if [ -n "$violations" ]; then
  echo "FAIL: internal/swarm must reach the agent runtime only through pkg/*."
  echo "These are DIRECT internal imports (route them through a pkg/* facade instead):"
  printf '%s\n' "$violations" | sed 's/^/  - /'
  exit 1
fi

echo "OK: internal/swarm directly imports only pkg/* (+ internal/swarm itself)."
