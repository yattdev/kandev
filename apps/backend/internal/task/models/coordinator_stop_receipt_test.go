package models

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopOperationSequenceJSONPreservesLegacyAndPrecision(t *testing.T) {
	cutoff := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, state, sequence string
		value                 *int64
	}{
		{name: "legacy unknown", state: "unknown"},
		{name: "known zero", state: "known", sequence: "0", value: func() *int64 { value := int64(0); return &value }()},
		{name: "signed limit", state: "known", sequence: "9223372036854775807", value: func() *int64 { value := int64(math.MaxInt64); return &value }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(CoordinatorStopOperation{ID: "stop-op", AdmissionCutoff: cutoff, AdmissionCutoffSequence: test.value})
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(encoded, &fields))
			require.Equal(t, "stop-op", fields["operation_id"])
			require.Equal(t, cutoff.Format(time.RFC3339), fields["admission_cutoff"])
			require.Equal(t, test.state, fields["admission_cutoff_sequence_state"])
			if test.value == nil {
				require.NotContains(t, fields, "admission_cutoff_sequence")
			} else {
				require.Equal(t, test.sequence, fields["admission_cutoff_sequence"])
			}
		})
	}
}
