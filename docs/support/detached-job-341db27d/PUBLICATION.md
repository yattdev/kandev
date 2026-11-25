# Exact qualification publication

Request `341db27d-80a1-482d-bbdb-2508e65ba996`.

Deployment evidence commit `649efca5287d6398e63766ee529f38968fb9347b` contains only this request's
generic receipt driver, reconciliation helper, tests and supported recipe.
`deployment-qualification.patch` is the exact zero-context patch from deployment
baseline `63f39df` and can be checked/applied with `git apply --unidiff-zero`.
The test sources are retained verbatim as `.py.txt` artifacts. No production
code/installed changes or owner acceptance is included in this publication.
The README and original result artifacts are copied byte-for-byte so their
recorded evidence hashes remain verifiable.

Canonical `upstream/main` was freshly fetched from
`https://github.com/kdlbs/kandev.git` and independently verified against remote
main at `14ad622733e3c9efb5c38538e1832a1938c37eb8` before this independent
publication branch. No unqualified origin/main base was used. The dirty primary
source checkout and all unrelated files and branches are preserved. Deployment
main's unrelated unpublished history is not pushed as a side effect.

The current installed broker/module hashes, actual public parser version,
qualified flags and precise caller-vs-server deadline distinction are recorded
in the README, result and raw synthetic outputs. The program is a Root-reviewed
recipe for an existing route, not a newly installed job API or server deadline.
Root owns the new populated-DB scenario and separately bound acceptance.
