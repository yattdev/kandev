package taskpreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type inspectFunc func(context.Context, Grant) error

func (f inspectFunc) Validate(c context.Context, g Grant) error { return f(c, g) }

type fixture struct {
	store     Store
	grant     Grant
	key       []byte
	clock     atomic.Int64
	allow     atomic.Bool
	container atomic.Bool
	checks    atomic.Int64
	upstream  *httptest.Server
	control   *Control
	gate      *Gateway
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: Store{Directory: t.TempDir()}, key: []byte(strings.Repeat("k", 32))}
	f.clock.Store(1700000000)
	f.allow.Store(true)
	f.container.Store(true)
	if err := os.Chmod(f.store.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.store.Directory, "nonces"), 0700); err != nil {
		t.Fatal(err)
	}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Cookie"), cookieName) || r.Header.Get("Authorization") != "" {
			t.Error("ingress credential reached trial")
		}
		w.Header().Add("Set-Cookie", cookieName+"=forged; Path=/")
		w.Header().Add("Set-Cookie", "trial=local; Path=/")
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "synthetic-trial:"+r.URL.Path)
	}))
	t.Cleanup(f.upstream.Close)
	u, _ := url.Parse(f.upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	f.grant = Grant{ID: "85278886-f97b-4219-b7ba-9182f7059c69", ApprovalID: "5047d7b6-8074-4e24-a025-7912eb4223d8", TaskID: "afdb2ef3-06ca-4cd5-a074-c4e691679da9", WorkspaceID: "2e62401b-5ffe-4050-bc1b-d49ea5d5dbcd", UserID: "owner", TaskRoot: "/data/tasks/fixture", CWD: "/data/tasks/fixture/ct", ContainerID: strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), Project: "kd_" + strings.Repeat("c", 24), Service: "core", HostPort: port, ContainerPort: 59430, MainOrigin: "https://main.example.test", PreviewOrigin: "https://isolated.example.test:59433", IssuedAt: f.clock.Load(), ExpiresAt: f.clock.Load() + 86400}
	f.grant.ApprovalSHA256 = strings.Repeat("a", 64)
	f.grant.ContainerStartedAt = time.Unix(f.clock.Load()-60, 0).UTC().Format(time.RFC3339Nano)
	f.grant.EnvironmentID = "0e3a05ec-e777-42a1-bdca-7dd4a8e75dbb"
	f.grant.ListenAddress = "127.0.0.1:59433"
	model, _ := json.Marshal(map[string]string{"task_root": f.grant.TaskRoot, "cwd": f.grant.CWD, "project": f.grant.Project})
	rootHash := sha256.Sum256([]byte(f.grant.TaskRoot))
	projectHash := sha256.Sum256([]byte(f.grant.Project))
	registry := filepath.Join(f.store.Directory, "agent-docker-models")
	f.grant.ModelPath = filepath.Join(registry, hex.EncodeToString(rootHash[:]), hex.EncodeToString(projectHash[:])+".json")
	f.grant.ClaimPath = filepath.Join(registry, "projects", hex.EncodeToString(projectHash[:])+".json")
	for _, path := range []string{f.grant.ModelPath, f.grant.ClaimPath} {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
	}
	sum := sha256.Sum256(model)
	f.grant.ModelSHA256 = hex.EncodeToString(sum[:])
	for _, p := range []string{f.grant.ModelPath, f.grant.ClaimPath} {
		if err := os.WriteFile(p, model, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.save(t)
	if err := os.WriteFile(filepath.Join(f.store.Directory, "key"), f.key, 0600); err != nil {
		t.Fatal(err)
	}
	f.control = &Control{Store: f.store, Now: f.now, Identity: func(r *http.Request) (Principal, bool) {
		user := r.Header.Get("Test-User")
		return Principal{UserID: user, SessionID: "native-session", Synthetic: user == "synthetic"}, user != ""
	}, Authorize: func(_ context.Context, user string, g Grant) error {
		f.checks.Add(1)
		if !f.allow.Load() || user != "owner" || g.TaskID != f.grant.TaskID || g.WorkspaceID != f.grant.WorkspaceID {
			return ErrDenied
		}
		return nil
	}}
	client := &http.Client{Timeout: time.Second, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != f.grant.MainOrigin+ControlPrefix+"check" {
			return nil, ErrDenied
		}
		w := httptest.NewRecorder()
		f.control.ServeHTTP(w, r)
		return w.Result(), nil
	})}
	f.control.Inspector = inspectFunc(func(_ context.Context, g Grant) error {
		if !f.container.Load() {
			return ErrDenied
		}
		b, _ := json.Marshal(map[string]any{"id": g.ContainerID, "image": g.ImageID, "running": true, "started_at": g.ContainerStartedAt, "labels": map[string]string{"com.docker.compose.project": g.Project, "com.docker.compose.service": g.Service}, "ports": map[string]any{"59430/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(g.HostPort)}}}})
		if err := ValidateInspection(b, g); err != nil {
			return err
		}
		return validateModel(g)
	})
	f.gate = &Gateway{Store: f.store, Now: f.now, Client: client}
	return f
}
func (f *fixture) now() time.Time { return time.Unix(f.clock.Load(), 0) }
func (f *fixture) save(t *testing.T) {
	t.Helper()
	b, _ := json.Marshal(f.grant)
	if e := os.WriteFile(filepath.Join(f.store.Directory, "manifest.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) session() string {
	return sign(f.key, "session", newClaims(f.grant, f.now(), Lifetime))
}
func (f *fixture) request(method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, f.grant.PreviewOrigin+path, nil)
	r.Header.Set("Origin", f.grant.PreviewOrigin)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		r.AddCookie(&http.Cookie{Name: "trial", Value: "local"})
	}
	r.Header.Set("Authorization", "must-not-reach-trial")
	w := httptest.NewRecorder()
	f.gate.ServeHTTP(w, r)
	return w
}

func TestAuthorizedTrialAndParkedLifetime(t *testing.T) {
	f := newFixture(t)
	token := f.session()
	first := f.request("GET", "/health", token)
	if first.Code != 200 || first.Body.String() != "synthetic-trial:/health" {
		t.Fatalf("authorized request: %d", first.Code)
	}
	if strings.Contains(first.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("app scripts would be blocked")
	}
	for _, c := range first.Result().Cookies() {
		if c.Name == cookieName || !c.Secure {
			t.Fatal("cookie isolation failed")
		}
	}
	// No executor/session lookup exists: parking does not revoke the registered
	// running container. Advance through its fixed, independently issued lifetime.
	f.clock.Add(86399)
	if f.request("GET", "/", token).Code != 200 {
		t.Fatal("parked grant expired early")
	}
	f.clock.Add(1)
	if f.request("GET", "/", token).Code != 403 {
		t.Fatal("grant survived fixed expiry")
	}
}

func TestNegativeAccessAndFreshAuthorization(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fixture, string) string
	}{
		{"unauthenticated", func(_ *fixture, _ string) string { return "" }},
		{"cross-user", func(f *fixture, _ string) string {
			c := newClaims(f.grant, f.now(), Lifetime)
			c.User = "other"
			return sign(f.key, "session", c)
		}},
		{"cross-workspace", func(f *fixture, s string) string {
			f.grant.WorkspaceID = "11111111-1111-4111-8111-111111111111"
			f.save(t)
			return s
		}},
		{"wrong-container", func(f *fixture, s string) string { f.container.Store(false); return s }},
		{"user-revoked", func(f *fixture, s string) string { f.allow.Store(false); return s }},
		{"grant-revoked", func(f *fixture, s string) string {
			if e := f.store.Revoke(); e != nil {
				t.Fatal(e)
			}
			return s
		}},
		{"wrong-purpose", func(f *fixture, _ string) string {
			return sign(f.key, "ticket", newClaims(f.grant, f.now(), time.Minute))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			token := tt.mutate(f, f.session())
			if f.request("GET", "/", token).Code != 403 {
				t.Fatal("negative access accepted")
			}
		})
	}
	f := newFixture(t)
	token := f.session()
	if f.request("GET", "/", token).Code != 200 {
		t.Fatal("initial access")
	}
	before := f.checks.Load()
	f.allow.Store(false)
	if f.request("GET", "/", token).Code != 403 || f.checks.Load() <= before {
		t.Fatal("authorization was cached")
	}
}

func TestOriginHostAndDuplicateCookieDenials(t *testing.T) {
	for _, name := range []string{"foreign-origin", "null-origin", "missing-post-origin", "wrong-host", "duplicate-cookie", "cross-site-navigation"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			r := httptest.NewRequest("GET", f.grant.PreviewOrigin+"/", nil)
			r.Header.Set("Origin", f.grant.PreviewOrigin)
			r.AddCookie(&http.Cookie{Name: cookieName, Value: f.session()})
			switch name {
			case "foreign-origin":
				r.Header.Set("Origin", f.grant.MainOrigin)
			case "null-origin":
				r.Header.Set("Origin", "null")
			case "missing-post-origin":
				r.Method = "POST"
				r.Header.Del("Origin")
			case "wrong-host":
				r.Host = "other.example.test"
			case "duplicate-cookie":
				r.AddCookie(&http.Cookie{Name: cookieName, Value: f.session()})
			case "cross-site-navigation":
				r.Header.Del("Origin")
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			w := httptest.NewRecorder()
			f.gate.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("origin or cookie boundary bypassed")
			}
		})
	}
}

func TestTicketHandoffAndPersistentReplayDenial(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("GET", f.grant.MainOrigin+ControlPrefix+"authorize", nil)
	r.Header.Set("Test-User", "owner")
	w := httptest.NewRecorder()
	f.control.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("native handoff denied")
	}
	parts := strings.Split(w.Body.String(), `name="ticket" value="`)
	if len(parts) != 2 {
		t.Fatal("no body-only ticket")
	}
	ticket := strings.Split(parts[1], `"`)[0]
	for i := 0; i < 2; i++ {
		r = httptest.NewRequest("POST", f.grant.PreviewOrigin+"/__preview/redeem", strings.NewReader(url.Values{"ticket": {ticket}}.Encode()))
		r.Header.Set("Origin", f.grant.MainOrigin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		f.gate.ServeHTTP(w, r)
		if i == 0 {
			if w.Code != 200 || !strings.Contains(w.Body.String(), `location.replace("/")`) || w.Header().Get("Location") != "" {
				t.Fatal("handoff failed")
			}
			cookie := w.Result().Cookies()[0]
			if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode || cookie.Expires.Unix() != f.grant.ExpiresAt {
				t.Fatal("unsafe session cookie")
			}
		} else if w.Code != 403 {
			t.Fatal("ticket replay accepted")
		}
		// The spent nonce remains on disk across a fresh gateway instance.
		copy := *f.gate
		f.gate = &copy
	}
}

func TestCredentialDeliveryRequiresExistingOwnerSession(t *testing.T) {
	f := newFixture(t)
	const body = `{"email":"synthetic@example.invalid","password":"synthetic-only"}`
	if e := os.WriteFile(filepath.Join(f.store.Directory, "login.json"), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	for _, user := range []string{"", "other", "synthetic", "owner"} {
		r := httptest.NewRequest("GET", f.grant.MainOrigin+ControlPrefix+"credentials", nil)
		r.Header.Set("Test-User", user)
		w := httptest.NewRecorder()
		f.control.ServeHTTP(w, r)
		if user == "owner" {
			if w.Code != 200 || w.Body.String() != body || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("authenticated credential delivery failed")
			}
		} else if w.Code != 403 || strings.Contains(w.Body.String(), "synthetic-only") {
			t.Fatal("login disclosed without existing owner session")
		}
	}
}

func TestOwnershipModelChangesAndPrivateFilesFailClosed(t *testing.T) {
	f := newFixture(t)
	if validateModel(f.grant) != nil {
		t.Fatal("valid model denied")
	}
	if e := os.WriteFile(f.grant.ModelPath, []byte(`{"task_root":"/data/tasks/other"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if validateModel(f.grant) == nil {
		t.Fatal("changed ownership accepted")
	}
	if e := os.Remove(filepath.Join(f.store.Directory, "key")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(f.grant.ClaimPath, filepath.Join(f.store.Directory, "key")); e != nil {
		t.Fatal(e)
	}
	if _, _, e := f.store.Load(); e == nil {
		t.Fatal("symlink key accepted")
	}
}

func TestActiveStreamClosedAfterRevocation(t *testing.T) {
	f := newFixture(t)
	f.upstream.Close()
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "started\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer f.upstream.Close()
	u, _ := url.Parse(f.upstream.URL)
	f.grant.HostPort, _ = strconv.Atoi(u.Port())
	f.save(t)
	server := httptest.NewTLSServer(f.gate)
	defer server.Close()
	client := server.Client()
	r, _ := http.NewRequest("GET", server.URL+"/stream", nil)
	r.Host = strings.TrimPrefix(f.grant.PreviewOrigin, "https://")
	r.Header.Set("Origin", f.grant.PreviewOrigin)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: f.session()})
	resp, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 8)
	if _, e = io.ReadFull(resp.Body, buf); e != nil {
		t.Fatal(e)
	}
	if e = f.store.Revoke(); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("revoked stream survived")
	}
}
