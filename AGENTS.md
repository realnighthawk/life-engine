# Implementation repository instructions

## Project mapping

- Documentation datastore: `kumarabd/project-hub`
- Project: `life-engine`
- Components implemented here: `engine`
- Project record in the datastore: `projects/life-engine/`
- Implementation repository: `realnighthawk/life-engine`
- Component source locations: `engine: repository root (cmd/, internal/, api/)`

## Project Hub is the documentation datastore

Project Hub is a shared, writable documentation datastore for projects. This repository contains implementation code; the Project Hub project record contains the requirements, architecture, specifications, decisions, concerns, implementation designs, and execution plans that implementation work reads and updates.

Use the Project Hub checkout configured for this implementation repository as the working datastore. Confirm its location, Git remote, branch, and working tree before use. Read and write the current project record there; do not copy canonical documents into this repository. If the checkout is unavailable, stop work that depends on project documentation and report the access limitation rather than creating a substitute record here.

## Start or resume work

1. Read this repository's applicable instructions and inspect its current branch, changes, and implementation state.
2. Access the current project record in the configured Project Hub checkout. Do not assume another repository's instructions are automatically loaded.
3. Read the project README, PRD, architecture, implementation index, applicable component designs under `implementation/<component>/`, referenced specs and ADRs, active concerns when present, and applicable system/component plans under `plans/`.
4. Reconstruct completed work, blockers, and the next executable work package from those documents and linked implementation evidence. Do not rely on conversation memory or treat scheduled work as completed.
5. Respect unresolved definition gates. If a required decision or document is missing, report the specific gap and continue only independent work supported by accepted decisions.

## Documentation ownership

Project Hub is the canonical home and shared datastore for requirements, architecture, implementation design, shared specifications, ADRs, concerns, roadmaps, and execution plans. Update those documents in place; do not create competing copies of them in this implementation repository.

This repository owns source code, tests, build/deployment configuration, and implementation evidence. Local setup/run instructions, code comments, and generated reference documentation may live here; link to Project Hub for canonical design and planning context.

Keep component implementation details in `implementation/<component>/` in Project Hub. Keep shared behavior in `specs/`, material accepted decisions in `adr/`, and work sequencing/status in `plans/`. Preserve the system implementation index and component/repository mapping as boundaries evolve.

## Execute and checkpoint

- Implement work in the canonical plan's dependency order and run checks appropriate to the change.
- Update the canonical component plan at each completed work package and before handing off: record verified completion, implementation commit or PR links, validation results or evidence links, blockers, and the next action.
- Update affected canonical designs/specs with finalized changes. Record material architecture or behavior decisions in an ADR; do not present an unresolved proposal as accepted.
- Keep the project README/current status and system plan consistent when milestone readiness changes. Never mark work complete solely because code was written.
- Commit scoped code changes in this repository and scoped documentation changes in Project Hub. Publish through each repository's permitted branch/PR workflow; obey branch protections and task authorization. Report both revisions and any unpublished changes. A local commit alone is not a completed GitHub update.
- Preserve unrelated user changes. Fetch/check for concurrent updates before publishing; reconcile them without force-pushing over others' work.

When a canonical document needs changing, edit the corresponding file in Project Hub, validate it there, and commit it in the Project Hub repository. Commit implementation code and tests in this repository. Keep those revisions separate and report both.

## Access failures and handoff

Access to Project Hub must support both reading and updating canonical documents. If access or publication is blocked, state the exact blocker and pending update. Do not silently create an alternate canonical plan elsewhere or claim the docs are synchronized. Continue only independent authorized work that does not require missing context; report pending documentation changes in the handoff until access is restored.

End each handoff with completed work, verification, code/documentation revisions, outstanding blockers, and the next executable action. Future sessions must resume from committed canonical state and implementation evidence.
