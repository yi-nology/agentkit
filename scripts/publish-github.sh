#!/usr/bin/env bash
# 将指定 tag 以 GitHub 模块身份发布到 GitHub 镜像仓库（双路径 go get 方案）。
#
# enjoye 仓库保持 canonical 不动；GitHub 侧每个版本是独立的合成孤儿 commit
# （module 行与仓库内引用改写为 GitHub 路径），各 tag 互不依赖，可对任意
# 历史 tag 重跑。日常发版在 push main+tag 到 origin 之后追加执行：
#
#   scripts/publish-github.sh v0.9.5
#
# 消费方随后即可 go get github.com/yi-nology/agentkit@vX.Y.Z。
set -euo pipefail

TAG="${1:?用法: publish-github.sh <tag>（如 v0.9.5）}"
GITHUB_MODULE="${GITHUB_MODULE:-github.com/yi-nology/agentkit}"
GITHUB_REMOTE="${GITHUB_REMOTE:-github}"

cd "$(dirname "$0")/.."
MODULE="$(sed -n 's/^module //p' go.mod)"
[ -n "${MODULE}" ] || { echo "无法从 go.mod 读取 module path" >&2; exit 1; }
if [ "${MODULE}" = "${GITHUB_MODULE}" ]; then
  echo "go.mod module 已是 ${GITHUB_MODULE}，双发脚本仅适用于 enjoye 身份仓库" >&2
  exit 1
fi

git rev-parse -q --verify "refs/tags/${TAG}^{commit}" >/dev/null \
  || { echo "tag 不存在: ${TAG}（先在 origin 完成正式发布）" >&2; exit 1; }

WORKTREE="$(mktemp -d "${TMPDIR:-/tmp}/agentkit-github.XXXXXX")"
trap 'git worktree remove --force "${WORKTREE}" 2>/dev/null; rm -rf "${WORKTREE}"' EXIT
git worktree add --detach "${WORKTREE}" "${TAG}" >/dev/null

# 模块身份改写：子包 import 前缀、文档、脚本引用随子串一并正确替换
grep -rl --exclude-dir=.git -F "${MODULE}" "${WORKTREE}" | while IFS= read -r f; do
  perl -pi -e "s{\Q${MODULE}\E}{${GITHUB_MODULE}}g" "${f}"
done

# 改写后必须可编译才允许推送
( cd "${WORKTREE}" && go build ./... )

TREE="$(git -C "${WORKTREE}" add -A && git -C "${WORKTREE}" write-tree)"
COMMIT="$(git commit-tree "${TREE}" -m "mirror ${TAG}: from ${MODULE}, rewritten as ${GITHUB_MODULE}")"
# 用 mktag 造附注 tag 对象,不落任何本地引用:worktree 与主仓库共享 refs,
# git tag -f 会把 canonical 仓库的同名 tag 覆盖成镜像 tag(2026-09 踩坑,
# 曾污染本地 v0.9.5~v0.10.3 六个 tag)。直接推对象 sha 到镜像远端。
TAGOBJ="$(printf 'object %s\ntype commit\ntag %s\ntagger %s\n\n%s\n' \
  "${COMMIT}" "${TAG}" "$(git var GIT_COMMITTER_IDENT)" \
  "${GITHUB_MODULE}@${TAG} (mirror of enjoye ${TAG})" | git mktag)"

# force 仅用于 GitHub 侧重跑同一 tag 的幂等发布;同 tag 内容(tree)确定性一致
git push "${GITHUB_REMOTE}" --force "${TAGOBJ}:refs/tags/${TAG}"
git push "${GITHUB_REMOTE}" --force "${COMMIT}:refs/heads/main"

echo "已发布 ${GITHUB_MODULE}@${TAG}"
echo "验证: go list -m ${GITHUB_MODULE}@${TAG}"
