package storeconformance

import (
	testconformance "github.com/kandev/kandev/internal/testutil/storeconformance"
	"testing"
)

// Keep the tagged fixtures immutable; seed the legacy gate on top of them.
func TestPreviousStableUpgradeCompletionGate(t *testing.T) {
	manifest := loadUpgradeManifest(t)
	for _, engine := range []testconformance.EngineName{testconformance.EngineSQLite, testconformance.EnginePostgres} {
		t.Run(string(engine), func(t *testing.T) {
			fixture := fixtureForEngine(t, manifest, engine)
			db := testconformance.OpenEngine(t, engine, "")
			if err := applyFixture(t, db, fixture); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB.Exec(`UPDATE workflow_steps SET name = 'Review' WHERE id = 'fixture-v0930-workflow-step'`); err != nil {
				t.Fatal(err)
			}
			if err := runCurrentInitialization(db); err != nil {
				t.Fatal(err)
			}
			var policy string
			if err := db.DB.Get(&policy, `SELECT completion_gate_policy FROM workflow_steps WHERE id = 'fixture-v0930-workflow-step'`); err != nil {
				t.Fatal(err)
			}
			if policy != "review" {
				t.Fatalf("migrated gate = %q, want review", policy)
			}
			if _, err := db.DB.Exec(`UPDATE workflow_steps SET name = 'Renamed check' WHERE id = 'fixture-v0930-workflow-step'`); err != nil {
				t.Fatal(err)
			}
			if err := runCurrentInitialization(db); err != nil {
				t.Fatal(err)
			}
			if err := db.DB.Get(&policy, `SELECT completion_gate_policy FROM workflow_steps WHERE id = 'fixture-v0930-workflow-step'`); err != nil {
				t.Fatal(err)
			}
			if policy != "review" {
				t.Fatalf("replayed gate = %q, want review", policy)
			}
		})
	}
}
