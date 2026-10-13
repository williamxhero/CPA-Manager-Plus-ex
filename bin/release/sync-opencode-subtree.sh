#!/usr/bin/env bash
set -euo pipefail

# SPEC5-B: OpenCode Go subtree refresh — the documented sync entry.
#
# Pulls the named upstream massiveits/opencode-go-cliproxyapi (branch main) into
# providers/opencode-go with `git subtree pull`, strips the build/workspace junk
# that `git subtree` cannot filter (plugins/**, worker*, *.log, prompt8-*.md),
# and re-runs the import-boundary check.
#
# It only operates on the CURRENT branch: it never merges or pushes a shared
# mainline, never deploys, and never reads real credentials. Run it on a fresh
# update branch (see docs/providers-layout-contract.md §8), then test before any
# merge.
#
# Conflict policy: local customizations (credential route, masked labels,
# host-relative auth paths, quota windows, catalog entries) must be preserved.
# If the pull reports conflicts, resolve them MANUALLY, keep our changes, then
# re-run this script and the Go tests. Never force-overwrite the customizations.
#
# Usage: bin/release/sync-opencode-subtree.sh

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFIX="providers/opencode-go"
REMOTE_NAME="opencode-upstream"
REMOTE_URL="https://github.com/massiveits/opencode-go-cliproxyapi.git"
UPSTREAM_BRANCH="main"

cd "$ROOT_DIR"
command -v git >/dev/null

if [[ -n "$(git status --porcelain -- "$PREFIX" providers/import-boundary.json)" ]]; then
  echo "工作区在 $PREFIX 或 import-boundary.json 上有未提交改动；请先提交或暂存。" >&2
  exit 2
fi

if ! git remote get-url "$REMOTE_NAME" >/dev/null 2>&1; then
  echo "[setup] 添加命名上游 remote $REMOTE_NAME -> $REMOTE_URL"
  git remote add "$REMOTE_NAME" "$REMOTE_URL"
fi

echo "[1/4] fetch $REMOTE_NAME"
git fetch "$REMOTE_NAME"

echo "[2/4] git subtree pull --prefix=$PREFIX $REMOTE_NAME $UPSTREAM_BRANCH --squash"
git subtree pull --prefix="$PREFIX" "$REMOTE_NAME" "$UPSTREAM_BRANCH" --squash \
  -m "chore(providers): sync OpenCode Go subtree from $REMOTE_NAME/$UPSTREAM_BRANCH (SPEC5-B)"

echo "[3/4] 移除 subtree 无法过滤的构建/工作区垃圾"
removed=0
if [[ -e "$PREFIX/plugins" ]]; then
  git rm -r -q --ignore-unmatch -- "$PREFIX/plugins"
  removed=1
fi
if [[ -e "$PREFIX/prompt8-credential-route.md" ]]; then
  git rm -q --ignore-unmatch -- "$PREFIX/prompt8-credential-route.md"
  removed=1
fi
while IFS= read -r -d '' junk; do
  git rm -q --ignore-unmatch -- "$junk"
  removed=1
done < <(find "$PREFIX" -type f \( -name '*.log' -o -name 'worker*' \) -print0 2>/dev/null)
if [[ "$removed" == 1 ]]; then
  git commit -q -m "chore(providers): drop excluded OpenCode subtree junk after sync (SPEC5-B)"
else
  echo "（无被排除路径需要移除）"
fi

echo "[4/4] 校验导入边界"
node bin/release/check-provider-import-boundary.mjs

echo "OK。下一步：cd $PREFIX && go test ./... ，然后按需提交并推送更新分支。"
echo "本脚本不 merge 主干、不 push、不部署；冲突须手动解决并保留本地定制。"
