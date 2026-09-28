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

## 5. Patch inventory

Keep this table current: it is the list of everything we would lose or send
upstream. One row per topic branch.

| Topic branch | What | Kind (config / plugin / new files / seam) | Upstream files touched | Upstream PR |
|---|---|---|---|---|
| `fork/unlisted-model` | Let a profile run a model id the CLI accepts but the ACP catalog does not list (e.g. `claude-opus-5-5`) | seam + flag | — | — |
| `fork/slack-notify` | Slack provider, step-change events, workspace → channel routing | new files + seam | — | — |
| `kandev-plugin-slack` fork | Spec questions posted to a Slack thread; the reply answers them | plugin | none | — |
| `fork/office-schedule` | Scheduled CEO/CTO agents via Office mode routines | config first; seams only if broken | — | — |

Fill in "upstream files touched" from `git diff --stat upstream/main...fork/<topic>`.
