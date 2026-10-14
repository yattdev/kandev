package taskpreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Gateway struct {
	Store  Store
	Client *http.Client
	Now    func() time.Time
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	safeHeaders(w)
	grant, key, err := g.Store.Load()
	if err != nil || !grant.Active(g.Now()) || "https://"+r.Host != grant.PreviewOrigin || r.URL.RawQuery != "" && strings.HasPrefix(r.URL.Path, "/__preview/") {
		denied(w)
		return
	}
	if r.URL.Path == "/__preview/redeem" {
		g.redeem(w, r, grant, key)
		return
	}
	if r.Header.Get("Service-Worker") != "" {
		denied(w)
		return
	}
	claims, err := g.browserSession(r, grant, key)
	if err != nil || g.authorize(r.Context(), grant, key, claims.User) != nil {
		denied(w)
		return
	}
	if r.URL.Path == "/__preview/revoke" {
		if r.Method != http.MethodPost || g.remoteAction(r.Context(), grant, key, claims.User, "revoke") != nil {
			denied(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/__preview/") {
		http.NotFound(w, r)
		return
	}
	g.proxy(w, r, grant, key)
}

func (g *Gateway) browserSession(r *http.Request, grant Grant, key []byte) (Claims, error) {
	if !allowedOrigin(r, grant.PreviewOrigin) {
		return Claims{}, ErrDenied
	}
	cookies := 0
	token := ""
	for _, cookie := range r.Cookies() {
		if cookie.Name == cookieName {
			cookies++
			token = cookie.Value
		}
	}
	claims, err := verify(key, "session", token, grant, g.Now())
	if cookies != 1 || err != nil {
		return Claims{}, ErrDenied
	}
	return claims, nil
}
func allowedOrigin(r *http.Request, origin string) bool {
	o := r.Header.Get("Origin")
	if o != "" {
		return o == origin
	}
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get("Upgrade") == "" && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func (g *Gateway) redeem(w http.ResponseWriter, r *http.Request, grant Grant, key []byte) {
	if r.Method != http.MethodPost || r.Header.Get("Origin") != grant.MainOrigin {
		denied(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || len(r.PostForm) != 1 || len(r.PostForm["ticket"]) != 1 {
		denied(w)
		return
	}
	claims, err := verify(key, "ticket", r.PostForm.Get("ticket"), grant, g.Now())
	if err != nil || claims.Expires-claims.Issued > 60 || g.authorize(r.Context(), grant, key, claims.User) != nil || g.Store.Redeem(claims) != nil {
		denied(w)
		return
	}
	session := sign(key, "session", newClaims(grant, g.Now(), Lifetime))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: session, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: time.Unix(grant.ExpiresAt, 0)})
	// Finish a document at the preview origin before navigating: a cross-site
	// 303 chain can suppress a newly issued SameSite=Strict cookie.
	script := `location.replace("/")`
	digest := sha256.Sum256([]byte(script))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'")
	_, _ = w.Write([]byte("<script>" + script + "</script>"))
}
func (g *Gateway) authorize(ctx context.Context, grant Grant, key []byte, user string) error {
	return g.remoteAction(ctx, grant, key, user, "check")
}
func (g *Gateway) remoteAction(ctx context.Context, grant Grant, key []byte, user, purpose string) error {
	claims := newClaims(grant, g.Now(), 15*time.Second)
	claims.User = user
	b, _ := json.Marshal(map[string]any{"proof": sign(key, purpose, claims), "revoke": purpose == "revoke"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, grant.MainOrigin+ControlPrefix+"check", bytes.NewReader(b))
	if err != nil {
		return ErrDenied
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.Client.Do(req)
	if err != nil {
		return ErrDenied
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return ErrDenied
	}
	return nil
}
func (g *Gateway) proxy(w http.ResponseWriter, r *http.Request, grant Grant, key []byte) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Unix(grant.ExpiresAt, 0).Sub(g.Now()))
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.watch(ctx, cancel, grant, key)
	}()
	defer func() { cancel(); <-done }()
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(grant.HostPort))
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		// The outer gate already validated the browser Origin. Normalise the
		// inner HTTP hop for the unchanged trial's same-origin/CSRF checks;
		// never require a trust-proxy setting or trial restart.
		p.Out.Host = target.Host
		if p.In.Header.Get("Origin") != "" {
			p.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
		p.Out.Header.Del("Authorization")
		p.Out.Header.Del("Proxy-Authorization")
		p.Out.Header.Del("Cookie")
		for _, cookie := range p.In.Cookies() {
			if cookie.Name != cookieName {
				p.Out.AddCookie(cookie)
			}
		}
		p.Out.Header.Del("Forwarded")
		p.Out.Header.Del("X-Forwarded-Host")
		p.Out.Header.Del("X-Forwarded-For")
		p.Out.Header.Del("X-Forwarded-Proto")
	}, ModifyResponse: func(resp *http.Response) error {
		if loc, err := url.Parse(resp.Header.Get("Location")); err == nil && loc.Host == target.Host {
			public, _ := url.Parse(grant.PreviewOrigin)
			loc.Scheme, loc.Host = public.Scheme, public.Host
			resp.Header.Set("Location", loc.String())
		}
		cookies := resp.Cookies()
		resp.Header.Del("Set-Cookie")
		for _, cookie := range cookies {
			if cookie.Name == cookieName || cookie.Domain != "" {
				continue
			}
			cookie.Secure = true
			resp.Header.Add("Set-Cookie", cookie.String())
		}
		resp.Header.Set("Cache-Control", "private, no-store")
		resp.Header.Set("Referrer-Policy", "no-referrer")
		resp.Header.Set("X-Content-Type-Options", "nosniff")
		resp.Header.Set("X-Frame-Options", "DENY")
		resp.Header.Add("Content-Security-Policy", "frame-ancestors 'none'")
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "preview upstream unavailable", http.StatusBadGateway)
	}, FlushInterval: -1}
	w.Header().Del("Content-Security-Policy")
	proxy.ServeHTTP(w, r.WithContext(ctx))
}
func (g *Gateway) watch(ctx context.Context, cancel context.CancelFunc, grant Grant, key []byte) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, _, err := g.Store.Load()
			if err != nil || current.Digest() != grant.Digest() || !grant.Active(g.Now()) || g.authorize(ctx, grant, key, grant.UserID) != nil {
				cancel()
				return
			}
		}
	}
}
