package taskpreview

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"net"
	"net/http"
	"time"
)

const ControlPrefix = "/api/v1/task-preview/"
const cookieName = "__Host-kandev-preview"

type Principal struct {
	UserID, SessionID string
	Synthetic         bool
}
type Authorize func(context.Context, string, Grant) error
type Control struct {
	Store     Store
	Inspector Inspector
	Authorize Authorize
	Identity  func(*http.Request) (Principal, bool)
	Now       func() time.Time
}

func safeHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
}
func denied(w http.ResponseWriter) {
	safeHeaders(w)
	http.Error(w, "preview access denied", http.StatusForbidden)
}
func (c *Control) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	safeHeaders(w)
	g, key, err := c.Store.Load()
	if err != nil || !g.Active(c.Now()) {
		denied(w)
		return
	}
	if r.URL.Path == ControlPrefix+"check" {
		c.check(w, r, g, key)
		return
	}
	if !c.browserAllowed(r, g) {
		denied(w)
		return
	}
	if !g.Active(c.Now()) || c.Store.Revoked() {
		denied(w)
		return
	}
	switch r.URL.Path {
	case ControlPrefix + "authorize":
		if r.Method != http.MethodGet || r.URL.RawQuery != "" {
			denied(w)
			return
		}
		c.handoff(w, g, key)
	case ControlPrefix + "credentials":
		c.credentials(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *Control) browserAllowed(r *http.Request, g Grant) bool {
	p, ok := c.Identity(r)
	if !ok || p.Synthetic || p.SessionID == "" || p.UserID != g.UserID || c.Authorize(r.Context(), p.UserID, g) != nil {
		return false
	}
	if !secureMainRequest(r) || "https://"+r.Host != g.MainOrigin || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != g.MainOrigin {
		return false
	}
	return c.Inspector != nil && c.Inspector.Validate(r.Context(), g) == nil
}
func (c *Control) credentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		denied(w)
		return
	}
	b, err := c.Store.read("login.json", 8192)
	if err != nil {
		http.Error(w, "owner login delivery pending", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// Only a local TLS terminator may assert the public scheme. A remote caller
// cannot use X-Forwarded-Proto to obtain a browser ticket over cleartext.
func secureMainRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}
func (c *Control) handoff(w http.ResponseWriter, g Grant, key []byte) {
	ticket := sign(key, "ticket", newClaims(g, c.Now(), time.Minute))
	script := "document.forms[0].submit()"
	digest := sha256.Sum256([]byte(script))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action "+g.PreviewOrigin+"; script-src 'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'")
	_, _ = w.Write([]byte(`<form method="post" action="` + html.EscapeString(g.PreviewOrigin) + `/__preview/redeem"><input type="hidden" name="ticket" value="` + html.EscapeString(ticket) + `"></form><script>` + script + `</script>`))
}
func (c *Control) check(w http.ResponseWriter, r *http.Request, g Grant, key []byte) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		denied(w)
		return
	}
	var req struct {
		Proof  string `json:"proof"`
		Revoke bool   `json:"revoke"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		denied(w)
		return
	}
	purpose := "check"
	if req.Revoke {
		purpose = "revoke"
	}
	claims, err := verify(key, purpose, req.Proof, g, c.Now())
	if err != nil || claims.Expires-claims.Issued > 15 || c.Authorize(r.Context(), claims.User, g) != nil ||
		c.Inspector == nil || c.Inspector.Validate(r.Context(), g) != nil {
		denied(w)
		return
	}
	if !g.Active(c.Now()) || c.Store.Revoked() {
		denied(w)
		return
	}
	if req.Revoke && c.Store.Revoke() != nil {
		denied(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
