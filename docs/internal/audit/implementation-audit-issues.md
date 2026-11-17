---
created_on: 2026-10-05 14:10
last_modified: 2026-10-05 14:10
status: current
---

# Implementation audit issue index

GitHub Issues is enabled for `alexgorbatchev/tuistory`. All 22 issues below are created, open, and verified with their expected titles, labels, and bodies. Each confirmed finding has its own defect report; security decisions, capacity hypotheses, and verification gaps remain separately classified.

Source: [implementation audit](implementation-audit.md), audited revision `31b03bebff37fa3ada24b5167b314ca5c4592f3e`. Issue #22 is the repair and due-diligence tracking checklist. No defect is marked fixed or landed by issue creation.

## Confirmed findings

| Audit ID | Issue | Title | Severity | Labels |
| --- | --- | --- | --- | --- |
| IA-001 | #1 | daemon-stop terminates unrelated listeners that share the relay port number | high | bug |
| IA-002 | #2 | Restart substitutes sh for the original executable and fails under restricted PATH | high | bug |
| IA-003 | #3 | Foreground passthrough resolves executables before applying the requested PATH and cwd | high | bug |
| IA-004 | #4 | Attach kill only sends SIGTERM and leaves resistant session processes alive | high | bug |
| IA-005 | #5 | wait-idle returns OK on timeout before the documented quiet interval | high | bug |
| IA-006 | #6 | Concurrent screenshots reuse millisecond filenames and overwrite other captures | medium | bug |
| IA-007 | #7 | Navigation key chords discard Ctrl and Shift modifiers | medium | bug |
| IA-008 | #8 | Literal wait strips printed block glyphs and fails to match visible text | medium | bug |
| IA-009 | #9 | click-at accepts negative coordinates and sends invalid SGR mouse events | medium | bug |
| IA-010 | #10 | Incremental read leaks ANSI fragments split across read boundaries | medium | bug |
| IA-011 | #11 | Embedded SKILL.md omits generated commands and has no live-interface drift check | medium | bug, documentation |
| IA-012 | #12 | No-argument commands accept extra input and still execute destructive actions | medium | bug |

## Due diligence and tracking

| ID | Issue | Title | Classification | Labels |
| --- | --- | --- | --- | --- |
| DD-001 | #13 | The relay trust boundary is unspecified while local callers can execute commands without credentials | Q-001: security decision | enhancement, question |
| DD-002 | #14 | Cross-platform builds leave native PTY and shutdown behavior unverified | verification follow-up | enhancement |
| DD-003 | #15 | Published archive installation and checksum behavior lack acceptance evidence | verification follow-up | enhancement |
| DD-004 | #16 | Per-axis terminal limits permit unmeasured aggregate allocations | source-grounded capacity assessment | enhancement |
| DD-005 | #17 | Relay JSON decoding has no body cap or server read deadlines | source-grounded capacity assessment | enhancement |
| DD-006 | #18 | Module checksum verification leaves dependency vulnerability exposure unassessed | verification follow-up | enhancement |
| DD-007 | #19 | Compiled E2E daemon omits race instrumentation during just check | verification follow-up | enhancement |
| DD-008 | #20 | Negotiated terminal key modes and rendering lack complete interoperability evidence | verification follow-up | enhancement |
| DD-009 | #21 | Audit repair tests need implementation-disabled sensitivity evidence | verification follow-up | enhancement |
| TRACK | #22 | Implementation-audit findings need a complete repair and due-diligence checklist | repair/checklist tracking | enhancement, documentation |

## Relationships and scope

- #17 depends on #13 before choosing request-abuse policy against the intended trust boundary. The reverse dependency is recorded in #13.
- #22 records completion dependencies on #1 through #21 and contains an unchecked issue checklist.
- Related PATH/structured-launch, terminal, documentation, and verification issues cross-reference one another by number.
- #14 accounts for both native platforms and current hosted CI enforcement. #15 covers published and candidate artifacts, checksums, installation, and embedded license accessibility.
- #16 and #17 preserve that no destructive capacity experiment or reproduced denial of service exists in the audit.
- #18 does not claim a known dependency vulnerability. #19 tracks real compiled-daemon race coverage; #20 tracks terminal interoperability; #21 tracks repair-test sensitivity.
- #22 accounts for reconciliation with `.tmp/migration-fixes-checklist.md`; verified repairs are checked off only after landing on main with commit evidence.

## Filing and validation evidence

- Repository inspection returns owner `alexgorbatchev`, ADMIN permission, and Issues initially disabled.
- `gh repo edit alexgorbatchev/tuistory --enable-issues` succeeds; subsequent repository inspection returns `hasIssuesEnabled: true`.
- Targeted open-issue searches for all audit symbols and due-diligence topics return no matches before filing. No unrelated issues are edited.
- Each issue is created sequentially using its prepared body file and labels from the repository's existing label list. Dependencies and tracking references are added after numbers are assigned.
- `gh issue view <number> --repo alexgorbatchev/tuistory --json number,title,state,labels,body` verifies all 22 issues against their prepared contents. ANSI evidence in #10 uses an explicit `<ESC>` notation.
- Fresh `just check` exits 0 at **92.46% statement coverage**; [execution log](../../../.tmp/implementation-audit/issues-just-check.log). `go mod tidy -diff` exits 0 with no diff; [execution log](../../../.tmp/implementation-audit/issues-tidy.log). The earlier audit's 92.50% result remains a separate recorded run.

Production code is unchanged by issue filing. The user instruction is recorded in root AGENTS.md, and the original audit report points to this index. Issue drafts are removed after remote verification; the verified issue metadata is retained under `.tmp/implementation-audit/created-issues.json`.

