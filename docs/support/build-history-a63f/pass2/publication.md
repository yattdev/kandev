# Publication

Exact a63f pass2 deployment source/evidence copied from main through `0eb92f4`.
Recovery helper commits: `28fa8df`, `85896ed`, `b98fe81`; applied result `0eb92f4`.
The unchanged earlier broker/module source patch remains in pass1. This update
adds historical proof and a Support-only exact-output migration; no product or
live lifecycle candidate is installed. Apply the deployment delta with
`git apply --unidiff-zero deployment-pass2.patch` only in the deployment repository.

Canonical upstream/main was fetched and remotely verified as
`df0786d954449f7b9af9d3bc7f9c2ff3d5b2a707`. Existing evidence branch and its
original canonical base are preserved; origin/main is not used as canonical.
This personal evidence publication does not merge the deployment's unrelated
unpublished commits. README/result/test evidence copies retain exact bytes.
