package provideraccess

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

func newGrantTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "provider-access.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetMaxOpenConns(8)
	store, err := NewStore(conn)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testGrant(id string) Grant {
	return Grant{
		ID: id, GrantScope: GrantScope{
			PluginInstallationID: "installation-1", PluginID: "coordinator",
			WorkspaceID: "workspace-1", ConversationKey: "coordinator",
			TargetTaskID: "target-1", RepositoryID: "repo-1", Provider: "github",
			Purpose: "actions_write",
		}, CreatedByUserID: "admin-1",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

func TestStoreReplaceGrantRevokesPreviousGeneration(t *testing.T) {
	store := newGrantTestStore(t)
	ctx := context.Background()
	first := testGrant("grant-1")
	if err := store.ReplaceGrant(ctx, &first); err != nil {
		t.Fatalf("replace first grant: %v", err)
	}
	if first.Generation != 1 {
		t.Fatalf("first generation = %d, want 1", first.Generation)
	}
	second := testGrant("grant-2")
	if err := store.ReplaceGrant(ctx, &second); err != nil {
		t.Fatalf("replace second grant: %v", err)
	}
	if second.Generation != 2 {
		t.Fatalf("second generation = %d, want 2", second.Generation)
	}
	previous, err := store.GetGrant(ctx, first.ID)
	if err != nil || previous == nil || previous.RevokedAt == nil {
		t.Fatalf("previous grant = %+v, err = %v, want revoked", previous, err)
	}
	active, err := store.GetActiveGrant(ctx, second.Scope())
	if err != nil || active == nil || active.ID != second.ID {
		t.Fatalf("active grant = %+v, err = %v, want second grant", active, err)
	}
	var receipts []auditRow
	if err := store.db.SelectContext(ctx, &receipts, `SELECT * FROM provider_access_audit ORDER BY at, id`); err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 3 {
		t.Fatalf("grant audit receipts = %d, want create, revoke, create", len(receipts))
	}
	outcomes := map[string]map[AuditOutcome]bool{}
	for _, receipt := range receipts {
		if outcomes[receipt.GrantID] == nil {
			outcomes[receipt.GrantID] = map[AuditOutcome]bool{}
		}
		outcomes[receipt.GrantID][AuditOutcome(receipt.Outcome)] = true
	}
	if !outcomes[first.ID][AuditGrantCreated] || !outcomes[first.ID][AuditGrantRevoked] ||
		!outcomes[second.ID][AuditGrantCreated] {
		t.Fatalf("grant audit outcomes = %+v", outcomes)
	}
}

func TestStoreListPluginGrantsIncludesOnlyExactPlugin(t *testing.T) {
	store := newGrantTestStore(t)
	ctx := context.Background()
	first := testGrant("grant-1")
	if err := store.ReplaceGrant(ctx, &first); err != nil {
		t.Fatal(err)
	}
	other := testGrant("grant-2")
	other.PluginID = "other-plugin"
	if err := store.ReplaceGrant(ctx, &other); err != nil {
		t.Fatal(err)
	}
	grants, err := store.ListPluginGrants(ctx, first.PluginID)
	if err != nil || len(grants) != 1 || grants[0].ID != first.ID {
		t.Fatalf("plugin grants = %+v, err = %v", grants, err)
	}
}

func TestStoreGrantAuditFailureRollsBackReplacementAndRevocation(t *testing.T) {
	store := newGrantTestStore(t)
	ctx := context.Background()
	first := testGrant("grant-1")
	if err := store.ReplaceGrant(ctx, &first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER reject_grant_audit
  BEFORE INSERT ON provider_access_audit
  BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	second := testGrant("grant-2")
	if err := store.ReplaceGrant(ctx, &second); err == nil {
		t.Fatal("replacement succeeded without its audit receipt")
	}
	if err := store.RevokeGrant(ctx, first.WorkspaceID, first.ID, time.Now().UTC()); err == nil {
		t.Fatal("revocation succeeded without its audit receipt")
	}
	active, err := store.GetActiveGrant(ctx, first.Scope())
	if err != nil || active == nil || active.ID != first.ID {
		t.Fatalf("grant after failed audit = %+v, err = %v", active, err)
	}
}

func TestStoreReplacementRejectsUnknownMintAfterRevocation(t *testing.T) {
	store := newGrantTestStore(t)
	ctx := context.Background()
	first := testGrant("grant-1")
	if err := store.ReplaceGrant(ctx, &first); err != nil {
		t.Fatal(err)
	}
	lease, err := store.IssueLease(ctx, testLeaseClaim(first))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimMintIntent(ctx, testMintClaim(first, lease)); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeGrant(ctx, first.WorkspaceID, first.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second := testGrant("grant-2")
	if err := store.ReplaceGrant(ctx, &second); err != ErrGrantUnavailable {
		t.Fatalf("replacement after ambiguous mint = %v, want unavailable", err)
	}
}

func TestStoreRevokeGrantFencesActiveScope(t *testing.T) {
	store := newGrantTestStore(t)
	ctx := context.Background()
	grant := testGrant("grant-1")
	if err := store.ReplaceGrant(ctx, &grant); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeGrant(ctx, "other-workspace", grant.ID, time.Now().UTC()); err == nil {
		t.Fatal("cross-workspace revocation succeeded")
	}
	if err := store.RevokeGrant(ctx, grant.WorkspaceID, grant.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	active, err := store.GetActiveGrant(ctx, grant.Scope())
	if err != nil || active != nil {
		t.Fatalf("active grant = %+v, err = %v, want none", active, err)
	}
	var outcomes []string
	if err := store.db.SelectContext(ctx, &outcomes, `SELECT outcome FROM provider_access_audit WHERE grant_id = ?`, grant.ID); err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 2 || !containsOutcome(outcomes, AuditGrantCreated) || !containsOutcome(outcomes, AuditGrantRevoked) {
		t.Fatalf("grant audit outcomes = %v", outcomes)
	}
}

func containsOutcome(outcomes []string, expected AuditOutcome) bool {
	for _, outcome := range outcomes {
		if outcome == string(expected) {
			return true
		}
	}
	return false
}

func TestStoreListWorkspaceGrantsKeepsWorkspaceBoundary(t *testing.T) {
	store := newGrantTestStore(t)
	ctx := context.Background()
	first := testGrant("grant-1")
	if err := store.ReplaceGrant(ctx, &first); err != nil {
		t.Fatal(err)
	}
	foreign := testGrant("foreign")
	foreign.WorkspaceID = "workspace-2"
	if err := store.ReplaceGrant(ctx, &foreign); err != nil {
		t.Fatal(err)
	}
	second := testGrant("grant-2")
	if err := store.ReplaceGrant(ctx, &second); err != nil {
		t.Fatal(err)
	}

	grants, err := store.ListWorkspaceGrants(ctx, first.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 || grants[0].ID != second.ID || grants[1].ID != first.ID ||
		grants[0].RevokedAt != nil || grants[1].RevokedAt == nil {
		t.Fatalf("workspace grants = %+v, want current then revoked generation", grants)
	}
	foreignGrants, err := store.ListWorkspaceGrants(ctx, foreign.WorkspaceID)
	if err != nil || len(foreignGrants) != 1 || foreignGrants[0].ID != foreign.ID {
		t.Fatalf("foreign grants = %+v, err = %v", foreignGrants, err)
	}
}

func TestStoreConcurrentGrantReplacementHasOneActiveGeneration(t *testing.T) {
	store := newGrantTestStore(t)
	const count = 8
	var wg sync.WaitGroup
	results := make(chan Grant, count)
	errors := make(chan error, count)
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			grant := testGrant(fmt.Sprintf("grant-%d", i))
			if err := store.ReplaceGrant(context.Background(), &grant); err != nil {
				errors <- err
				return
			}
			results <- grant
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("replace grant: %v", err)
	}
	if t.Failed() {
		return
	}
	close(results)
	seen := map[int64]bool{}
	for grant := range results {
		if seen[grant.Generation] {
			t.Fatalf("duplicate generation %d", grant.Generation)
		}
		seen[grant.Generation] = true
	}
	if len(seen) != count {
		t.Fatalf("generations = %d, want %d", len(seen), count)
	}
	active, err := store.GetActiveGrant(context.Background(), testGrant("sample").Scope())
	if err != nil || active == nil || active.Generation != count {
		t.Fatalf("active grant = %+v, err = %v, want generation %d", active, err, count)
	}
}
