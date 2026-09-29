# Fork guide: zakaria1193/kandev

This is a fork of [kdlbs/kandev](https://github.com/kdlbs/kandev). It exists to
run a spec-driven pipeline on a homelab (Slack per project, spec questions in
Slack threads, scheduled CEO/CTO agents, any model id). It must stay **cheap to
rebase on upstream, often** (target: weekly, in minutes). Every rule below
serves that.

Read this before changing anything. Upstream's `AGENTS.md` still applies in
full; this file only adds constraints.

## 1. Prefer, in this order

1. **Configuration.** Workflows, steps, agent profiles, labels, prompts and
   notification providers are data: set them through the MCP/settings API or
   an imported workflow document, never by changing code.
2. **A plugin**, in its own repository (`zakaria1193/kandev-plugin-<slug>`,
   or a fork of an official `kdlbs/kandev-plugin-*`). See
   `docs/public/plugins-authoring.md`. A plugin never conflicts with a rebase.
3. **New files in core**: a new package, provider, handler or component that
   plugs into an existing registry or extension point.
4. **Editing upstream files**: only the few lines needed to register or call
   the new code (the "seam"). Last resort.

If a feature needs a lot of (4), stop and look for an upstream extension point
or propose one upstream instead.

## 2. Rules for touching upstream files

- **Small, local seams.** A registration line, one `if` behind a flag, one new
  field. No refactors, renames, moves, reformatting, import reordering or lint
  "cleanups" of code we did not write.
- **Mark every seam** with a comment containing `fork:` and the topic, e.g.
  `// fork(slack-notify): register Slack provider`. `git grep -n 'fork('`
  must list every place where we diverge.
- **Default off.** New behaviour sits behind a setting or a feature flag
  (`KANDEV_FEATURES_*` pattern) whose default keeps upstream behaviour, so
  upstream tests keep passing unchanged.
- **Additive schema only.** New DB columns/tables go in new migration files;
  never edit an upstream migration. New JSON fields are `omitempty`.
- **Do not touch** lockfiles, generated files, `CHANGELOG.md`, version files,
  CI workflows or release config unless the change is impossible without it.
- **Tests live next to the new code**, in new files where possible
  (`*_fork_test.go`, `*.fork.test.ts`), not appended to upstream test files.
- **UI**: new components in new files; the seam into an upstream component is
  one import + one render line.

## 3. Branches and commits

- `upstream/main` is never committed to. `main` on this fork is
  `upstream/main` + our topic branches, rebuilt by rebasing.
- One topic branch per feature, based on `upstream/main`:
  `fork/<topic>` (e.g. `fork/unlisted-model`, `fork/slack-notify`).
  Each topic must build and pass its tests on its own, so it can be dropped,
  reordered or sent upstream as a PR.
- Commits follow upstream's conventional commits (`feat(scope): ...`,
  enforced by commitlint). Keep a topic to a few commits; squash fixups
  before rebasing.

## 4. Rebasing on upstream

```bash
git fetch upstream
for b in $(git for-each-ref --format='%(refname:short)' refs/heads/fork/); do
  git rebase upstream/main "$b" || break     # fix conflicts at the seams only
done
git checkout main && git reset --hard upstream/main
for b in $(git for-each-ref --format='%(refname:short)' refs/heads/fork/); do git merge --no-ff "$b"; done
make -C apps/backend test && (cd apps && pnpm -r test)   # then deploy
```

A conflict outside a `fork(` seam means a rule above was broken: fix the
patch so the next rebase is clean, not just this one.

## 5. CI on the fork

Workflows that need upstream-only secrets, deploy targets, registries or
bots are **disabled on the fork** (a repo setting, so the workflow files stay
untouched): Published Docs, Plugin Registry Index / Curated Release Poll /
Star Refresh, release, universal-rebuild, Update managed runtime pins,
CI base image (pushes to `ghcr.io/kdlbs`), Claude Code, Claude Code Review,
OpenCode Code Review, PR Walkthrough (+ Reconcile), Preview Environment.
List or undo with `gh workflow list --all` / `gh workflow enable <name>`.

Everything else must be green on `main`. After a rebase and push:

```bash
gh run list -R zakaria1193/kandev -b main -L 15    # Backend/Frontend/E2E take ~30 min
gh run view <id> -R zakaria1193/kandev --log-failed
```

A red run is either ours (fix it in the topic branch) or also red upstream
(`gh run list -R kdlbs/kandev --commit <sha>`); never fix it by editing a
workflow. `AGENTS.md` is at upstream's 300-line harness limit: add nothing
to it, the FORK.md pointer lives on its existing Purpose line.

## 6. Patch inventory

Keep this table current: it is the list of everything we would lose or send
upstream. One row per topic branch.

| Topic branch | What | Kind (config / plugin / new files / seam) | Upstream files touched | Upstream PR |
|---|---|---|---|---|
| `fork/unlisted-model` | Let a profile run a model id the CLI accepts but the ACP catalog does not list (e.g. `claude-opus-5-5`): lifecycle tries it before the fallback rules, and agentctl stops refusing it locally so the agent decides | seam + flag `KANDEV_FORK_UNLISTED_MODELS` (default off) | `lifecycle/start_model.go` (4 lines), `agentctl/server/adapter/transport/acp/adapter_session.go` (2 lines) | — |
| `fork/slack-notify` | Slack provider (`type: slack`, channel per workspace id/name, token via secret/env), `task.step_entered` + `task.completed` events derived from existing bus events, clarification question text + in-memory Slack ts index; the new events are listed in the settings UI only with flag `KANDEV_FORK_SLACK_EVENTS=true` (default off), always subscribable via the API | new files + seam | `internal/notifications/service/service.go` (+4/−1), `internal/backendapp/gateway.go` (1 line), `internal/backendapp/main.go` (+1) | — |
| `kandev-plugin-slack` fork (`feat/clarification-threads`) | Agent questions posted to the workspace's Slack channel as a thread; an allow-listed reply answers them (via `POST /api/v1/clarification/<id>/respond` + a PAT, since plugins may not answer) | plugin | none (optional ~15-line seam in `plugins/host_interactions.go` would drop the PAT) | — |
| `fork/office-schedule` | Scheduled Office agents (CEO/CTO): answer permission requests of taskless run sessions (flag `KANDEV_FORK_OFFICE_TASKLESS_PERMISSIONS`, default off; auto-approve candidates only), and resolve the managed npm prefix before the routing recovery probe (plain bug fix, upstreamable) | new files + seam | `internal/agent/runtime/lifecycle/manager_events.go` (+1), `internal/agent/runtime/routingerr/acp_probe.go` (+4) | — |

Fill in "upstream files touched" from `git diff --stat upstream/main...fork/<topic>`.
