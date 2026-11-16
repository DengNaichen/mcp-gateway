---
name: git-workflow
description: "管理 mcp-gateway 的分支、.worktrees/ 隔离工作区、依赖与端口准备、提交、GitHub PR 和合并后清理。开始任何代码修改、创建或移除 worktree、提交、开 PR、合并或整理分支时使用。"
---

# Git Workflow

本 skill 拥有 `mcp-gateway` 的 Git 与 worktree 操作流程。远端为 GitHub `DengNaichen/mcp-gateway`（`origin`），使用 `gh` 操作，首次使用前可检查 `gh auth status`。任务与 Spec 登记规则见 [AGENTS.md](../../../AGENTS.md)。

## 开始前检查

修改前先核对当前状态，并 fetch `origin/main`：

```sh
git status --short --branch
git worktree list --porcelain
git fetch --no-tags origin +refs/heads/main:refs/remotes/origin/main
```

- 目标 worktree dirty、分叉或基线不明时，先弄清归属；不静默 `stash`、覆盖或混入他人工作。
- 主 checkout 已有未提交改动时不要强行切换分支；新开功能分支请直接在 `.worktrees/` 创建独立 worktree。

## 分支

- `main` 是唯一长期分支，只通过 PR 修改；不直接在 `main` 上开发或提交功能代码。
- 一个独立交付对应一个分支、一个 PR、最多一个可写 worktree。命名采用 `<type>/<short-name>`（如 `feat/...`、`fix/...`、`refactor/...`、`docs/...`、`chore/...`），小写、连字符分隔。
- 有实现依赖时使用堆叠分支（以前置分支为基线）；前置 PR squash 合并后，用 `git rebase --onto origin/main <前置分支合并前 head> <当前分支>` 接回 `main`，核对 PR diff 无多余提交后再推送。
- 分支合并后即删除，**不复用**。由于仓库采用 squash 合并，分支提交不会成为 `main` 的祖先，不可用 `git branch -d` 或文件名/年龄推断已合并，须通过 PR 记录确认（见[合并后清理](#合并后清理)）。

## Worktree

### 位置与归属

- **主 checkout**（`git worktree list --porcelain` 第一项，即 `~/Developer/mcp-gateway`）停在干净的 `main`，只用于同步、清理与默认启动。
- **手工、脚本及 Herdr 创建的所有 worktree 统一收拢在 `<主 checkout>/.worktrees/<branch-slug>`**（分支名中的 `/` 替换为 `-`），已在 `.gitignore` 中忽略（`/.worktrees/`）。
  - **严禁**将 worktree 平放在 `~/Developer/`（如 `../mcp-gateway-*`）或 `/tmp` 下。
  - 若使用 `herdr worktree create` 直接创建，必须显式传入 `--path <主 checkout>/.worktrees/<branch-slug>`；推荐优先运行 `scripts/new-worktree` 后再挂入 Herdr：
    ```sh
    scripts/new-worktree <branch>
    herdr worktree open --cwd <主 checkout> --path <输出路径> --label <name> --no-focus
    ```
- **宿主自行管理的 worktree**（如 Antigravity swarm 的 `~/.gemini/antigravity-cli/worktrees/`、Claude Code `.claude/worktrees/`、Codex `~/.codex/worktrees/`）由宿主管理位置与生命周期，不受 `.worktrees/` 位置限制，但同样须运行 setup 与提交前验证。

### 创建与初始化

```sh
scripts/new-worktree <branch> [--base <ref>] [--no-setup]
```

实现见 [scripts/new-worktree](../../../scripts/new-worktree) 与 [scripts/setup-worktree](../../../scripts/setup-worktree)：

- 默认基线为 `origin/main`；脚本只 fetch 所选基线分支，fetch 失败立即中止，**不使用过期本地缓存**。
- 新分支以 `--no-track` 创建，不把 `origin/main` 继承为 upstream；首次推送使用 `git push -u origin <branch>`。
- 已有本地分支直接检出，不要传 `--base`；若分支已在其他 worktree 检出则报错并打印现有路径。
- 创建后自动运行 `scripts/setup-worktree --if-needed`（纯文档任务可传 `--no-setup`）：
  - Go module/build cache 跨 worktree 原生共享，无需重复下载。
  - 为次级 worktree 生成独占端口槽位与数据目录（见下节）。

### 运行时隔离

实现位于 [scripts/worktree-runtime.mjs](../../../scripts/worktree-runtime.mjs)：

- **主 checkout 保持默认端口与目录**：后端/桌面端 `127.0.0.1:8789`、数据目录 `data/dev`，不生成 `.env.worktree`。
- **次级 worktree** 根据其 canonical 路径派生稳定 12 位 hex ID 与独占槽位 `1–99`，写入 Git 忽略的 `.env.worktree`（权限 `0600`）：
  - 后端地址 `GATEWAY_DEV_ADDR=127.0.0.1:<8800+slot>`（避开 Stable `8788` 与 Dev `8789`）
  - 本地数据目录 `GATEWAY_DEV_DATA_DIR=<worktree>/data/dev`
- 若 `.env.worktree` 已存在且不含托管 marker（手工创建文件），脚本拒绝覆盖或删除。
- 需要多个 worktree 并行启动服务时使用：
  ```sh
  node scripts/worktree-runtime.mjs run api
  node scripts/worktree-runtime.mjs run desktop
  ```
- Go 单元测试（`t.TempDir()`）与验证脚本（`127.0.0.1:0`）天然隔离，可直接运行，不占用固定槽位端口。

### 清理

- 未合并或放弃的 worktree：确认工作区干净且无占用进程后，执行：
  ```sh
  node scripts/worktree-runtime.mjs clean <worktree路径>
  git -C <主 checkout> worktree remove <worktree路径>
  ```
- **严禁使用 `git worktree remove --force`**，不静默丢弃未提交或未推送改动。
- 停止本会话启动的后台进程只按 PID 精准停止，不使用宽泛的 `pkill -f` 误杀其他 worktree 或主 checkout 服务。

## 提交与验证

按改动范围运行对应验证（对齐 CI）：

```sh
CGO_ENABLED=0 go test -count=1 ./...
python3 scripts/check-doc-links.py
node --test scripts/release-policy.test.mjs scripts/worktree-runtime.test.mjs
```

- 修改原生桌面端或发布脚本时按需运行 `make package`、`scripts/verify-desktop-theme.py`、`scripts/verify-build-channels.py`。
- 逐文件 `git add <path>`，不用 `git add -A` / `git add .`，避免混入无关改动或 `.DS_Store`。

## Pull Request

- 显式推送分支：`git push -u origin <branch>`。
- 按 [AGENTS.md](../../../AGENTS.md) 同步更新 Roster 任务记录（关联仓库、实际分支与 PR 链接）。
- 使用 `gh pr create --repo DengNaichen/mcp-gateway --base main --head <branch>` 创建 PR，写明改动内容、实际运行的验证命令与结果。
- **“开 PR”与“合并”是两次授权**：仅要求创建 PR 时，交付 PR 链接后停止，不自动合并或开启 auto-merge。

## 合并后清理

squash 合并后从干净的**主 checkout** 运行 [scripts/finish-branch](../../../scripts/finish-branch)：

```sh
scripts/finish-branch <branch> --dry-run
scripts/finish-branch <branch>
```

脚本按顺序执行以下核对与清理（任一步不满足即停止）：
1. 查询 `gh pr list --repo DengNaichen/mcp-gateway --head <branch> --state merged`，确认存在唯一已合入 PR。
2. 校验本地分支与 `origin/<branch>` 的 SHA 都严格等于 PR 的 `headRefOid`。
3. 确认目标 worktree 无未提交或未跟踪文件，且主 checkout 处于干净的 `main`，fast-forward 到 `origin/main` 并验证 `mergeCommit` 已进入 `origin/main`。
4. 运行 `node scripts/worktree-runtime.mjs clean <worktree>` 并 `git worktree remove <worktree>`。
5. 删除本地分支（`git branch -D`）并以 `--force-with-lease=<branch>:<headRefOid>` 删除远端分支，最后 `git fetch --prune` 并输出最新 `git worktree list`。
