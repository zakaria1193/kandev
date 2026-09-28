---
id: "02-touch-file-actions"
title: "Keep touch file actions available"
status: done
wave: 2
depends_on: []
plan: "plan.md"
requirements:
  - REQ-UI-CHANGES-FILE-ROW-CONTAINMENT-002
acceptance_criteria:
  - AC-UI-CHANGES-FILE-ROW-CONTAINMENT-002.2
  - AC-UI-CHANGES-FILE-ROW-CONTAINMENT-002.5
system_design:
  - ../../specs/ui/system-design/changes-file-row-containment.md
---

# Task 02: Keep Touch File Actions Available

## Summary

Keep the existing working-tree file action menu available after a touch user
opens it, so the user can select an action after the menu finishes opening.
Preserve the current action set, row behavior, and fine-pointer and keyboard
activation.

## In scope

- Use one transient touch open transition for the trigger's pointer sequence.
- Prevent menu-trigger events from activating the file row.
- Assert the menu remains available after its entrance animation and that Edit
  opens the selected file.

## Out of scope

- Adding or removing file actions, changing their Git semantics, or changing
  persistent file-list preferences.
- Changing keyboard, fine-pointer, outside-dismissal, or Escape behavior.

## Acceptance

- Touch activation opens the menu once and leaves it visible through its opening
  animation until action selection or intentional dismissal.
- The file viewer stays closed while the menu is open; selecting Edit opens the
  viewer for the selected path.
- The action trigger never activates the underlying file row.

## ASCII UI preview

UI-02: Phone Changes, touch file-action menu open.

```text
Changes
  [file icon] src/example.ts       [more]
             +1  -0
    +--------------------------+
    | Copy path                |
    | Stage                    |
    | Edit                     |
    | Discard                  |
    +--------------------------+
```

After the user taps `[more]`, the menu stays open until the user selects an
action or dismisses it. The exact menu placement is illustrative; all actions
remain touch-accessible.

## Verification

```bash
(cd apps/web && TMPDIR=/var/tmp pnpm e2e:docker --no-build --project mobile-chrome -- tests/git/mobile-symlink-identification.spec.ts --retries=0 --workers=1 --reporter=line)
(cd apps/web && TMPDIR=/var/tmp pnpm e2e:docker --no-build --project mobile-chrome -- tests/git/mobile-symlink-identification.spec.ts --retries=0 --workers=1 --repeat-each=5 --reporter=line)
(cd apps/web && pnpm exec eslint components/task/changes-panel-touch-file-row.tsx e2e/tests/git/mobile-symlink-identification.spec.ts)
(cd apps/web && pnpm exec prettier --check components/task/changes-panel-touch-file-row.tsx e2e/tests/git/mobile-symlink-identification.spec.ts)
(cd apps/web && pnpm run typecheck)
python3 scripts/list-docs.py validate
python3 scripts/lint-spec-files.test.py
python3 scripts/lint-spec-files.py --all
git diff --check
```

## Files likely touched

- `apps/web/components/task/changes-panel-touch-file-row.tsx`
- `apps/web/e2e/tests/git/mobile-symlink-identification.spec.ts`
- `docs/specs/ui/requirements/changes-file-row-containment.md`
- `docs/specs/ui/system-design/changes-file-row-containment.md`
- `docs/plans/changes-file-row-containment/plan.md`
- `docs/plans/changes-file-row-containment/task-02-touch-file-actions.md`

## Dependencies

None.

## Risks

- Touch pointers must activate the controlled menu once, while mouse, keyboard,
  selection, outside dismissal, and Escape retain the shared primitive's
  behavior.

## Parallelism

`sequential`

## Inputs

- `REQ-UI-CHANGES-FILE-ROW-CONTAINMENT-002.2` and `.5`.
- The touch file-row action implementation and mobile symlink E2E fixture.
- CI trace for E2E workflow run `36366862304` on pre-fixup head
  `98f8a73712a467ecc33e41f1699882272b845a92`.

## Results

Completed on 2026-09-28.

- CI trace reproduced the failure: the action menu appeared after the row-action
  tap and disappeared before the Edit selection.
- Added an explicit post-animation assertion for the menu and Edit item. The
  mobile symlink E2E passed once after a fresh build, then five consecutive
  times with retries disabled. It asserts that the viewer remains closed before
  selection and opens after Edit.
- The touch trigger now controls one open transition across pointer-down,
  pointer-up, and click, while non-touch activation remains with DropdownMenu.
- Changed-file ESLint and Prettier checks and web TypeScript typecheck passed.
- `python3 scripts/list-docs.py validate` passed (320 decisions and 1,218
  specifications); the specification linter's 36 tests and full catalog lint
  passed. `node --test .github/scripts/pr-docs.test.cjs` passed 101 tests.
- The PR documentation-coverage preflight classified the changed component as
  covered by this work order. Normal commit hooks passed without bypass.
