## Fixed during review

* [apps/web/e2e/scripts/run-e2e.sh:196](apps/web/e2e/scripts/run-e2e.sh:196) — Managed `containers` runs now export `KANDEV_E2E_CONTAINERS=1` through both host and Docker paths, so global setup verifies the Linux helpers before fixtures use them (commit 3c19aeedd).
