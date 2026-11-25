# Publication binding

Source refactor: deployment `cb2ba91`; installed evidence: `63f39df62b937b6c35b0b2b08046281cb6d0d365`.
`deployment-pass1.patch` is the exact zero-context delta from `681f606` through
that evidence commit, limited to this incident. Apply/check with
`git apply --unidiff-zero` after the initial `deployment-repair.patch` in the
parent evidence folder. New test sources are retained verbatim in this folder;
initial test/source artifacts remain in the parent for historical verification.
Deployment main's unrelated unpublished history is not pushed.

This existing source publication branch retains canonical verified base
`9ee282c20a36b71d1d8ebae797865741bdc94a38`; no new worktree or product source
change is made in this escalation. The primary dirty source checkout, earlier
Support branches and unrelated local files are preserved.
