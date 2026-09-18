---
name: "User Guide: Diff"
description: Discover what changed in the exo CLI since the last user guide update and produce an update plan.
category: User Guide
tags: [user-guide, diff, changelog]
---
**Purpose**

Analyse the exo-cli repo diff between the version documented in the user guide and the current version on `main`, then produce a structured plan of what needs to be added, updated, or deleted in the user guide.

**Guardrails**
- Do NOT edit any user guide files during this skill. Output is a plan only.
- Refer to `openspec/project.md` for project conventions (version mention requirement, repo locations).
- If you cannot determine the previous CLI version from the user guide, ask the user.
- If the exo-cli repo is not found at `../exo-cli` (relative to the website repo root), ask the user for the path.
- **Keep it user-oriented.** This is a user guide, not a changelog. Only document changes that affect what users see or do. Skip:
  - Bug fixes that just make things work as users already expect (e.g., "auto-quoting special characters" — users expect search to just work)
  - Internal refactors, code restructuring, or implementation details (e.g., "uses atomic counters", "consolidates grants by bucket prefix")
  - Performance optimizations that are invisible to the user
  - Debug/internal environment variables or flags
  - Developer-facing details (JSON wire formats, internal file paths, library internals like libmagic or git-annex modes)
  - When in doubt, ask: "would a user need to change their behavior or learn something new?" If not, skip it.

**Steps**

1. **Locate the exo-cli repo.**
   - Default path: `../exo-cli` relative to the website repo root (see `openspec/project.md`).
   - Verify the repo exists and is on the `main` branch. If not on `main`, warn the user and ask whether to proceed.

2. **Pull the latest version of main.**
   - Ensure the local `main` branch is up to date: `git -C <exo-cli-path> pull --rebase`.
   - Also fetch all tags: `git -C <exo-cli-path> fetch --tags`.
   - If the pull fails (e.g., tracking branch mismatch), set the upstream and retry:
     `git -C <exo-cli-path> branch --set-upstream-to=origin/main main && git -C <exo-cli-path> pull --rebase`.

3. **Determine the current CLI version.**
   - Read the tag at HEAD of the exo-cli `main` branch (`git -C <exo-cli-path> describe --tags --abbrev=0`).
   - This is the **target version**.

4. **Determine the previous CLI version.**
   - Search the user guide index (`content/docs/user-guide/_index.md`) for a version string (pattern: `v\d+\.\d+\.\d+`).
   - If not found, scan other user guide files for a version reference.
   - If still not found, ask the user for the previous version.
   - This is the **base version**.

5. **Generate the CLI diff.**
   - Run `git -C <exo-cli-path> diff <base-version>..<target-version> --stat` for an overview.
   - Run `git -C <exo-cli-path> log <base-version>..<target-version> --oneline` for commit summaries.
   - For files with significant changes, read the full diff (`git -C <exo-cli-path> diff <base-version>..<target-version> -- <file>`).
   - Focus on: new commands, changed flags/options, new config files, new env vars, changed behaviors, removed features.

6. **Map changes to user guide sections.**
   - Read each user guide file under `content/docs/user-guide/` to understand what is currently documented.
   - For each CLI change, determine which user guide section(s) it affects.
   - Classify each change as one of:
     - **Add** — new content needed (new feature, new command, new config file)
     - **Update** — existing content needs modification (changed behavior, new flag, updated output)
     - **Delete** — content should be removed (removed feature, deprecated command)

7. **Produce the update plan.**
   - Output a structured plan with this format:

   ```
   # User Guide Update Plan: <base-version> → <target-version>

   ## Summary
   <1-3 sentence overview of the main changes>

   ## Files to Modify

   ### <N>. `<file-path>` — <short description>
   - **Lines <range>**: <what to change and why>
   - ...

   ## Files NOT Changed
   - `<file>` — <reason no changes needed>

   ## Verification
   - Run `hugo` to confirm the site builds without errors
   - Visually check that new sections render correctly
   ```

   - For each file, reference specific line numbers and describe the change precisely enough that an implementer can apply it without re-reading the CLI diff.
   - Include the actual content to add when possible (YAML examples, table rows, command output).
   - Always include the version stamp update in `_index.md`.
   - The plan will also serve as the commit body. Structure the "Files to Modify" section as a concise, numbered checklist so it reads well in `git log`.

8. **Commit convention.**
   - When the user guide changes are implemented and the user asks to commit, use the plan's "Files to Modify" summary as the commit body so the changelog is self-documenting.
   - **Prefix rule** — compare the base and target semver versions to pick the prefix:
     - **Patch bump** (e.g., `0.39.1` → `0.39.2`): use `fix(docs):`
     - **Minor or major bump** (e.g., `0.39.0` → `0.40.0`): use `feat(docs):`
   - Format: `<prefix> update user guide for exo CLI <base> → <target>`

**Tips**
- Look at CLI source files like `cmd/*.go` for new/changed commands and flags.
- Look at config structs (e.g., `*config*.go`, `*grants*.go`, `*permissions*.go`) for new configuration fields.
- Look at `main.go` or `version.go` for version constants.
- Check `README.md` or `CHANGELOG.md` in the exo-cli repo for high-level summaries.
- New env vars are typically registered in `cmd/root.go` or similar entry points.
- When a diff is large, prioritize user-facing changes over internal refactors.
