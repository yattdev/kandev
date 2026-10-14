package httpmw

import (
	"net/http"
	"testing"
)

func TestTaskPreviewSelfAuthenticatedAllowlistIsExact(t *testing.T) {
	if !isPublicPath(http.MethodPost, "/api/v1/task-preview/check") {
		t.Fatal("signed control checks must reach their authenticator")
	}
	for _, path := range []string{"/api/v1/task-preview/authorize", "/api/v1/task-preview/credentials", "/api/v1/task-preview/check/other"} {
		if isPublicPath(http.MethodPost, path) || isPublicPath(http.MethodGet, path) {
			t.Fatal("browser or adjacent path bypassed session authentication")
		}
	}
	if isPublicPath(http.MethodGet, "/api/v1/task-preview/check") {
		t.Fatal("unsigned GET check allowlisted")
	}
}
