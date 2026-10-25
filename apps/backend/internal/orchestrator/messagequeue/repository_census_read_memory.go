package messagequeue

import (
	"context"
	"encoding/json"
	"sort"
	"unicode/utf8"
)

type censusLimitWriter struct{ remaining int }

func (w *censusLimitWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, ErrCensusTooLarge
	}
	w.remaining -= len(p)
	return len(p), nil
}

func (r *memoryRepository) ActiveQueueCensus(ctx context.Context, proof ActiveQueueCensusProof, capacity int) (*ActiveQueueCensus, error) {
	identity := proof.Identity
	if identity.TaskID == "" || identity.SessionID == "" || identity.SessionIncarnationID == "" || proof.ExecutionID == "" || proof.WorkspaceID == "" {
		return nil, ErrSessionIdentityMismatch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.validateMemoryCensusAuthority(ctx, identity); err != nil {
		return nil, err
	}
	entries, err := r.readCensusMemory(identity)
	if err != nil {
		return nil, err
	}
	if err := r.validateMemoryCensusAuthority(ctx, identity); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newActiveQueueCensus(identity, capacity, entries)
}

func (r *memoryRepository) validateMemoryCensusAuthority(ctx context.Context, identity QueueSessionIdentity) error {
	if r.authority == nil {
		return ErrSessionIdentityMismatch
	}
	current, err := r.authority(ctx, identity.TaskID, identity.SessionID)
	if err != nil {
		return err
	}
	if current != identity {
		return ErrSessionIdentityMismatch
	}
	return nil
}

func (r *memoryRepository) readCensusMemory(identity QueueSessionIdentity) ([]ActiveQueueCensusEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.identities[identity.SessionID]
	if !ok || stored != identity {
		return nil, ErrSessionIdentityMismatch
	}
	rows := append([]*QueuedMessage(nil), r.entries[identity.SessionID]...)
	if len(rows) > maxCensusExamined {
		return nil, ErrCensusTooLarge
	}
	for _, row := range rows {
		if row == nil {
			return nil, ErrSessionIdentityMismatch
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Position != rows[j].Position {
			return rows[i].Position < rows[j].Position
		}
		return rows[i].ID < rows[j].ID
	})
	entries := make([]ActiveQueueCensusEntry, 0, len(rows))
	budget := maxCensusBytes
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		entry, visible, err := censusMemoryRow(row, identity, seen, &budget, len(entries)+1)
		if err != nil {
			return nil, err
		}
		if visible {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func censusMemoryRow(row *QueuedMessage, identity QueueSessionIdentity, seen map[string]struct{}, budget *int, ordinal int) (ActiveQueueCensusEntry, bool, error) {
	if row.TaskID != identity.TaskID || row.SessionID != identity.SessionID || row.Position <= 0 {
		return ActiveQueueCensusEntry{}, false, ErrSessionIdentityMismatch
	}
	if row.ID == "" || !utf8.ValidString(row.ID) || len(row.ID) > maxCensusIDBytes {
		return ActiveQueueCensusEntry{}, false, ErrCensusTooLarge
	}
	if _, exists := seen[row.ID]; exists {
		return ActiveQueueCensusEntry{}, false, ErrSessionIdentityMismatch
	}
	seen[row.ID] = struct{}{}
	metadataBytes := 2 // Empty metadata is stored as {} by the SQL provider.
	if len(row.Metadata) > 0 {
		// json.Encoder adds one newline that is not part of the stored metadata.
		writer := &censusLimitWriter{remaining: maxCensusMetadataBytes + 1}
		if err := json.NewEncoder(writer).Encode(row.Metadata); err != nil {
			return ActiveQueueCensusEntry{}, false, ErrCensusTooLarge
		}
		metadataBytes = maxCensusMetadataBytes - writer.remaining
	}
	*budget -= len(row.Content) + metadataBytes
	if *budget < 0 {
		return ActiveQueueCensusEntry{}, false, ErrCensusTooLarge
	}
	if row.IsReservedInFlight() && (!row.IsDurablePlanComment() || row.IsDeliveryAttempted()) {
		return ActiveQueueCensusEntry{}, false, nil
	}
	if ordinal > maxCensusVisible {
		return ActiveQueueCensusEntry{}, false, ErrCensusTooLarge
	}
	entry, err := censusEntry(row.ID, row.Content, ordinal)
	return entry, err == nil, err
}
