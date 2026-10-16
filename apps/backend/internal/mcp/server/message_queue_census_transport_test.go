package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type cappedQueueRequestReader struct {
	first bool
}

func (r *cappedQueueRequestReader) Read(p []byte) (int, error) {
	if r.first {
		return 0, errors.New("request reader budget exceeded")
	}
	r.first = true
	return copy(p, `{"jsonrpc":"2.0","method":"other"}`), nil
}

func TestMessageQueueBoundaryLeavesLargeNonQueueBodyToDownstream(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Body = io.NopCloser(&cappedQueueRequestReader{})
	request.ContentLength = 1 << 20
	called := false
	boundary := queueArgumentBoundaryHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		prefix := make([]byte, 32)
		_, err := r.Body.Read(prefix)
		require.NoError(t, err)
		w.WriteHeader(http.StatusAccepted)
	}))
	response := httptest.NewRecorder()
	boundary.ServeHTTP(response, request)
	require.True(t, called, "the boundary must not consume a non-queue body before the MCP handler")
	require.Equal(t, http.StatusAccepted, response.Code)
}

func TestMessageQueueRawRemovalArgumentsRetainByteAndDuplicateBounds(t *testing.T) {
	base := `{"entry_id":"a","claim":"` + strings.Repeat("a", 64) + `"}`
	for _, size := range []int{2048, 2049} {
		arguments := base[:16] + strings.Repeat(" ", size-len(base)) + base[16:]
		err := validateQueueToolRawArguments(removeClaimedQueueTool, json.RawMessage(arguments))
		if size == 2048 {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	duplicate := `{"entry_id":"a","entry_id":"a","claim":"` + strings.Repeat("a", 64) + `"}`
	require.Error(t, validateQueueToolRawArguments(removeClaimedQueueTool, json.RawMessage(duplicate)))
}
