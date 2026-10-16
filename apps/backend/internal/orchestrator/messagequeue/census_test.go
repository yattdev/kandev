package messagequeue

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPendingCensusFinalizeProducesStableClaim(t *testing.T) {
	queuedAt := time.Date(2026, time.October, 16, 16, 0, 0, 123000000, time.UTC)
	census := &PendingCensus{
		Version:             censusSnapshotVersion,
		Identity:            QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"},
		OriginalExecutionID: "execution",
		Revision:            7,
		Count:               2,
		Capacity:            10,
		Entries: []PendingCensusEntry{
			{ID: "first", Position: 1, QueuedBy: QueuedByUser, QueuedAt: queuedAt, ContentHash: testCensusDigest("c"), ContentSize: 1, ClaimSHA256: testCensusDigest("a")},
			{ID: "second", Position: 2, QueuedBy: QueuedByAgent, QueuedAt: queuedAt, ContentHash: testCensusDigest("d"), ContentSize: 1, ClaimSHA256: testCensusDigest("b")},
		},
	}

	require.NoError(t, census.Finalize())
	firstClaim := census.SnapshotSHA256
	require.Len(t, firstClaim, 64)
	require.NoError(t, census.Finalize())
	require.Equal(t, firstClaim, census.SnapshotSHA256)
}

func TestPendingCensusEmptySnapshotRejectsInvalidClaimIdentity(t *testing.T) {
	valid := PendingCensus{
		Version: censusSnapshotVersion,
		Identity: QueueSessionIdentity{
			TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation",
		},
		OriginalExecutionID: "execution",
		Entries:             []PendingCensusEntry{},
	}
	require.NoError(t, valid.Finalize())
	for _, invalid := range []string{strings.Repeat("x", censusClaimFieldLimit+1), string([]byte{0xff})} {
		for _, mutate := range []func(*PendingCensus){
			func(c *PendingCensus) { c.Identity.TaskID = invalid },
			func(c *PendingCensus) { c.Identity.SessionID = invalid },
			func(c *PendingCensus) { c.Identity.SessionIncarnationID = invalid },
			func(c *PendingCensus) { c.OriginalExecutionID = invalid },
		} {
			snapshot := valid
			mutate(&snapshot)
			require.Error(t, snapshot.Finalize())
		}
	}
}

func TestPendingCensusFinalizeRejectsInvalidEntryFields(t *testing.T) {
	valid := PendingCensus{
		Version: censusSnapshotVersion,
		Identity: QueueSessionIdentity{
			TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation",
		},
		OriginalExecutionID: "execution",
		Count:               1,
		Entries: []PendingCensusEntry{{
			ID: "entry", Position: 1, QueuedBy: QueuedByUser, QueuedAt: time.Now().UTC(),
			ContentHash: testCensusDigest("c"), ClaimSHA256: testCensusDigest("a"),
		}},
	}
	require.NoError(t, valid.Finalize())
	for _, invalid := range []string{strings.Repeat("x", censusClaimFieldLimit+1), string([]byte{0xff})} {
		for _, mutate := range []func(*PendingCensusEntry){
			func(entry *PendingCensusEntry) { entry.ID = invalid },
			func(entry *PendingCensusEntry) { entry.QueuedBy = invalid },
		} {
			snapshot := valid
			snapshot.Entries = append([]PendingCensusEntry(nil), valid.Entries...)
			mutate(&snapshot.Entries[0])
			require.Error(t, snapshot.Finalize())
		}
	}
}

func TestPendingCensusFinalizeRejectsIncompleteOrUnorderedEvidence(t *testing.T) {
	valid := PendingCensus{
		Version:             censusSnapshotVersion,
		Identity:            QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"},
		OriginalExecutionID: "execution",
		Count:               1,
		Entries: []PendingCensusEntry{{
			ID: "entry", Position: 1, QueuedBy: QueuedByUser, QueuedAt: time.Now().UTC(), ContentHash: testCensusDigest("c"), ContentSize: 1, ClaimSHA256: testCensusDigest("a"),
		}},
	}
	require.NoError(t, valid.Finalize())

	missingExecution := valid
	missingExecution.OriginalExecutionID = ""
	require.Error(t, missingExecution.Finalize())

	unordered := valid
	unordered.Count = 2
	unordered.Entries = append(unordered.Entries, PendingCensusEntry{
		ID: "earlier", Position: 1, QueuedBy: QueuedByUser, QueuedAt: time.Now().UTC(), ContentHash: testCensusDigest("d"), ContentSize: 1, ClaimSHA256: testCensusDigest("b"),
	})
	require.Error(t, unordered.Finalize())
}

func testCensusDigest(prefix string) string {
	return prefix + "000000000000000000000000000000000000000000000000000000000000000"
}

func TestNewPendingCensusEntryProjectsOnlyVisibleEvidence(t *testing.T) {
	entry, err := NewPendingCensusEntry(
		QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}, "execution", 7, QueuedMessage{
			ID: "entry", Position: 9, QueuedBy: QueuedByUser,
			QueuedAt: time.Date(2026, time.October, 16, 16, 1, 2, 3, time.FixedZone("offset", 3600)),
			Content:  "secret", Model: "model", Attachments: []MessageAttachment{{Name: "secret.txt"}},
			Metadata: map[string]interface{}{"secret": "value"},
		})
	require.NoError(t, err)
	require.Equal(t, "entry", entry.ID)
	require.Equal(t, int64(9), entry.Position)
	require.Equal(t, QueuedByUser, entry.QueuedBy)
	require.Equal(t, "c2bd181bbda92f49a68a19896a375d2f377bee30f616a24cb0200f9517a931a1", entry.ClaimSHA256)
}

func TestCensusEntryClaimUsesFrozenFiveFieldEncoding(t *testing.T) {
	identity := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}
	claim, err := CensusEntryClaim(identity, "execution", "entry", 7)
	require.NoError(t, err)
	require.Equal(t, "c2bd181bbda92f49a68a19896a375d2f377bee30f616a24cb0200f9517a931a1", claim)

	changedExecution, err := CensusEntryClaim(identity, "execution-2", "entry", 7)
	require.NoError(t, err)
	require.NotEqual(t, claim, changedExecution)
	_, err = CensusEntryClaim(identity, "execution", "entry", -1)
	require.Error(t, err)
	_, err = CensusEntryClaim(identity, "execution", string(make([]byte, 257)), 7)
	require.Error(t, err)
}
