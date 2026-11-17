#!/usr/bin/env bash
# Preflight for changing terraform-provider-gpupaas. Prints OK / NOTE / MISSING lines.
# Exit 1 if something required is missing. Safe to run any time: it only reads.
# Works from any directory: it finds its own repo from the script's location.
cd "$(dirname "$0")/../../.." 2>/dev/null && git rev-parse --git-dir >/dev/null 2>&1 || { echo "MISSING  cannot find the repo root from this script (expected .claude/skills/<name>/preflight.sh)"; exit 2; }

bad=0
ok()   { echo "OK       $1"; }
note() { echo "NOTE     $1"; }
miss() { echo "MISSING  $1"; bad=1; }

if [ -d ../gpupaas-go ]; then
  ok "SDK checkout ../gpupaas-go (branch $(git -C ../gpupaas-go branch --show-current))"
else
  miss "SDK checkout ../gpupaas-go: clone gpupaas-ai/gpupaas-go next to this repo"
fi

if command -v go >/dev/null 2>&1; then ok "go $(go version | awk '{print $3}')"; else miss "go"; fi

if command -v terraform >/dev/null 2>&1; then
  ok "terraform $(command -v terraform)"
elif command -v tofu >/dev/null 2>&1; then
  ok "tofu found; run acceptance tests with TF_ACC_TERRAFORM_PATH=$(command -v tofu)"
else
  miss "terraform or tofu on PATH (the TF_ACC tests need one)"
fi

if command -v tfplugindocs >/dev/null 2>&1; then
  ok "tfplugindocs"
else
  note "tfplugindocs not on PATH: go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.20.1, or run make with TFPLUGINDOCS=/path/to/tfplugindocs"
fi

if grep -q '=> ../gpupaas-go' go.mod; then
  ok "go.mod uses the local SDK (replace => ../gpupaas-go). Never commit it in a release."
else
  note "go.mod has no local replace: the provider builds against a pinned SDK tag, not your SDK changes"
fi

branch="$(git branch --show-current)"
[ -n "$branch" ] || branch="(detached HEAD)"
case "$branch" in
  main|master|release/*) note "on branch $branch: work on a feature branch" ;;
  *) ok "branch $branch" ;;
esac

if [ -n "$(git status --porcelain)" ]; then
  note "uncommitted changes present:"
  git status --short | head -5 | sed 's/^/           /'
else
  ok "working tree clean"
fi

if go build ./... >/dev/null 2>&1; then
  ok "go build ./... passes"
else
  miss "go build ./... fails: fix the build before starting"
fi

echo
if [ "$bad" = 0 ]; then echo "PREFLIGHT PASS"; else echo "PREFLIGHT FAIL"; fi
exit "$bad"
