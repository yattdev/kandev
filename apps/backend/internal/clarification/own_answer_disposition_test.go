package clarification

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/events"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

// Contract coverage for the existing supported Human rejection path; this test
// adds no new settlement behavior or agent authority.
func TestResolverOwnAnswerDispositionRejectsOnlyOriginalWithoutResume(t *testing.T) {
	const pending = "original-pending"
	original := resolverDeliveryMessage(pending, "original-message", "original-turn")
	original.Content = "Original question body"
	r, _, repo, _, eventBus := newResolverDeliveryFixture(t, pending, []*taskmodels.Message{original})
	other := resolverDeliveryMessage("other-pending", "other-message", "other-turn")
	repo.messages["other-pending"] = []*taskmodels.Message{other}
	beforeOther := cloneResolverMessage(other)
	result, claimed, err := r.ResolveBundle(context.Background(), pending, Outcome{Rejected: true, Answers: []Answer{}})
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, string(StatusRejected), result.Status)
	require.True(t, result.Response.Rejected)
	require.Empty(t, eventBus.resumeRequests)
	require.Equal(t, "Original question body", repo.messages[pending][0].Content)
	require.Equal(t, beforeOther, repo.messages["other-pending"][0])
	for _, event := range eventBus.events {
		require.NotEqual(t, events.ClarificationPrimaryAnswered, event.Type)
	}
	_, claimed, err = r.ResolveBundle(context.Background(), pending, Outcome{Rejected: true, Answers: []Answer{}})
	require.NoError(t, err)
	require.False(t, claimed)
	require.Empty(t, eventBus.resumeRequests)
}
