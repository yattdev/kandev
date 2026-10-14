package taskpreview

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserRevocationUsesAuthoritativeBackend(t *testing.T) {
	f := newFixture(t)
	if f.request("POST", "/__preview/revoke", f.session()).Code != 204 {
		t.Fatal("authorized backend revoke failed")
	}
	if !f.store.Revoked() || f.request("GET", "/", f.session()).Code != 403 {
		t.Fatal("revoke did not close access")
	}
}

func TestGatewayBlocksServiceWorkerRegistration(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("GET", f.grant.PreviewOrigin+"/worker.js", nil)
	r.Header.Set("Origin", f.grant.PreviewOrigin)
	r.Header.Set("Service-Worker", "script")
	r.AddCookie(&http.Cookie{Name: cookieName, Value: f.session()})
	w := httptest.NewRecorder()
	f.gate.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("application worker could intercept the authorization handoff")
	}
}
