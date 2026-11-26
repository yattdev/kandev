# Publication and disposition

Request `806cdc24-d005-4605-b6ca-fd7ee61f21fe`, escalation pass 1:
**RESOLVED for the installed selected-build receipt adapter.**

[README.md](README.md) is the current supported recipe and qualification report.
It supersedes the first-pass BLOCKED disposition in the parent folder. The
source evidence showing that bare Compose cannot enter the old hot-module
path remains valid; the new explicit adapter uses that existing authenticated
module path without replacing the broker.

This directory mirrors deployment-relative operator documentation. Run operator
installation/rollback utilities only from the deployment checkout and layout,
never from this publication mirror or an agent task. The owner-facing command
is the exact broker recipe in README.md; Root retains separate acceptance.

Deployment implementation commit:
`c4f5b4e` (full hash in source-commit.txt).
Deployment installed-evidence commit: `6ab463c`.
[deployment-source.patch](deployment-source.patch) contains only the requested
module repair, its synthetic tests and pre-install review/rollback package.
Unrelated unpublished deployment commits and user edits were not pushed.

The installed operations SHA256 is
`1f075423e4c89e32caa359e2b27e12086aad4d0adc1f5e4eafe54052a1e7608a`.
Actual public-file custody/hashes, unchanged container identity, automatic
approval-reviewed installation receipt, 87 installed passing private cases,
rollback details and exact finite owner acceptance are included. Source,
installation and real owner acceptance are reported separately. No real owner
build, metadata query, source/data access, historical restoration or app/test
execution occurred in this Support request.
