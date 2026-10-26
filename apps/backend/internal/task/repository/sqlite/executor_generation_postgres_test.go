package sqlite

import (
	"testing"

	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPostgresUpsertExecutorRunningRejectsUnknownGenerationForSameExecution(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertUpsertExecutorRunningRejectsUnknownGenerationForSameExecution(t, repo)
}
