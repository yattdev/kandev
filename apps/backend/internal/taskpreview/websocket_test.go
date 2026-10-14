package taskpreview

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketAuthorizedThenExpiredOrRevoked(t *testing.T) {
	for _, reason := range []string{"expired", "revoked"} {
		t.Run(reason, func(t *testing.T) {
			f := newFixture(t)
			f.upstream.Close()
			f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The pinned trial's normal same-origin check must succeed on the
				// inner HTTP hop without changing its trust-proxy configuration.
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				if err = conn.WriteMessage(websocket.TextMessage, []byte("synthetic")); err != nil {
					return
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer f.upstream.Close()
			u, _ := url.Parse(f.upstream.URL)
			f.grant.HostPort, _ = strconv.Atoi(u.Port())
			server := httptest.NewTLSServer(f.gate)
			defer server.Close()
			f.grant.PreviewOrigin = server.URL
			f.grant.ListenAddress = strings.TrimPrefix(server.URL, "https://")
			f.save(t)
			dialer := websocket.Dialer{TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig}
			headers := http.Header{"Origin": {f.grant.PreviewOrigin}, "Cookie": {cookieName + "=" + f.session()}}
			conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https")+"/ws", headers)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if _, body, e := conn.ReadMessage(); e != nil || string(body) != "synthetic" {
				t.Fatal("authorized upgrade failed")
			}
			if reason == "expired" {
				f.clock.Store(f.grant.ExpiresAt)
			} else if e := f.store.Revoke(); e != nil {
				t.Fatal(e)
			}
			if e := conn.SetReadDeadline(time.Now().Add(4 * time.Second)); e != nil {
				t.Fatal(e)
			}
			if _, _, e := conn.ReadMessage(); e == nil {
				t.Fatal("terminal access retained")
			}
		})
	}
}

func TestInspectionRejectsTargetSubstitution(t *testing.T) {
	f := newFixture(t)
	valid := `{"id":"` + f.grant.ContainerID + `","image":"` + f.grant.ImageID + `","running":true,"started_at":"` + f.grant.ContainerStartedAt + `","labels":{"com.docker.compose.project":"` + f.grant.Project + `","com.docker.compose.service":"core"},"ports":{"59430/tcp":[{"HostIp":"127.0.0.1","HostPort":"` + strconv.Itoa(f.grant.HostPort) + `"}]}}`
	if ValidateInspection([]byte(valid), f.grant) != nil {
		t.Fatal("valid inspection denied")
	}
	for _, pair := range [][2]string{{f.grant.ContainerID, strings.Repeat("d", 64)}, {f.grant.ImageID, "sha256:" + strings.Repeat("e", 64)}, {f.grant.Project, "kd_" + strings.Repeat("f", 24)}, {`"running":true`, `"running":false`}, {`127.0.0.1`, `0.0.0.0`}} {
		if ValidateInspection([]byte(strings.ReplaceAll(valid, pair[0], pair[1])), f.grant) == nil {
			t.Fatal("substituted target accepted")
		}
	}
}

func TestMainCleartextAndForwardedSchemeDenials(t *testing.T) {
	f := newFixture(t)
	for _, peer := range []string{"192.0.2.1:1234", "127.0.0.1:1234"} {
		r := httptest.NewRequest("GET", "http://main.example.test"+ControlPrefix+"authorize", nil)
		r.RemoteAddr = peer
		r.Header.Set("Test-User", "owner")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		f.control.ServeHTTP(w, r)
		if peer == "192.0.2.1:1234" && w.Code != 403 {
			t.Fatal("remote cleartext handoff accepted")
		}
		if peer == "127.0.0.1:1234" && w.Code != 200 {
			t.Fatal("local TLS terminator denied")
		}
	}
}
