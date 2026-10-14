// Package taskpreview implements an operator-registered single-target preview.
package taskpreview

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Lifetime = 24 * time.Hour

var ErrDenied = errors.New("preview access denied")
var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var noncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var projectPattern = regexp.MustCompile(`^kd_[a-f0-9]{24}$`)
var servicePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Grant is operator-installed; HTTP callers cannot select or register targets.
type Grant struct {
	ID                 string `json:"id"`
	ApprovalID         string `json:"approval_id"`
	ApprovalSHA256     string `json:"approval_sha256"`
	EnvironmentID      string `json:"environment_id"`
	TaskID             string `json:"task_id"`
	WorkspaceID        string `json:"workspace_id"`
	UserID             string `json:"user_id"`
	TaskRoot           string `json:"task_root"`
	CWD                string `json:"cwd"`
	ContainerID        string `json:"container_id"`
	ContainerStartedAt string `json:"container_started_at"`
	ImageID            string `json:"image_id"`
	Project            string `json:"project"`
	Service            string `json:"service"`
	HostPort           int    `json:"host_port"`
	ContainerPort      int    `json:"container_port"`
	ModelPath          string `json:"model_path"`
	ClaimPath          string `json:"claim_path"`
	ModelSHA256        string `json:"model_sha256"`
	MainOrigin         string `json:"main_origin"`
	PreviewOrigin      string `json:"preview_origin"`
	ListenAddress      string `json:"listen_address"`
	IssuedAt           int64  `json:"issued_at"`
	ExpiresAt          int64  `json:"expires_at"`
}

func (g Grant) Validate() error {
	for _, id := range []string{g.ID, g.ApprovalID, g.EnvironmentID, g.TaskID, g.WorkspaceID} {
		if !uuidPattern.MatchString(id) {
			return ErrDenied
		}
	}
	main, err := origin(g.MainOrigin)
	if err != nil {
		return err
	}
	preview, err := origin(g.PreviewOrigin)
	if err != nil || strings.EqualFold(main.Hostname(), preview.Hostname()) {
		return ErrDenied
	}
	if !privateListener(g.ListenAddress, preview.Port()) || !digestPattern.MatchString(g.ApprovalSHA256) {
		return ErrDenied
	}
	if g.validateTarget() != nil || g.validatePaths() != nil || !g.validContainerStart() || g.IssuedAt <= 0 || g.ExpiresAt-g.IssuedAt != int64(Lifetime/time.Second) {
		return ErrDenied
	}
	return nil
}

func (g Grant) validContainerStart() bool {
	started, err := time.Parse(time.RFC3339Nano, g.ContainerStartedAt)
	return err == nil && started.Unix() > 0 && started.Unix() <= g.IssuedAt
}

func validPort(port int) bool { return port >= 1024 && port <= 65535 }
func (g Grant) validateTarget() error {
	if g.UserID == "" || len(g.UserID) > 128 || !digestPattern.MatchString(g.ContainerID) ||
		!strings.HasPrefix(g.ImageID, "sha256:") || !digestPattern.MatchString(strings.TrimPrefix(g.ImageID, "sha256:")) ||
		!projectPattern.MatchString(g.Project) || !servicePattern.MatchString(g.Service) ||
		!validPort(g.HostPort) || !validPort(g.ContainerPort) || !digestPattern.MatchString(g.ModelSHA256) {
		return ErrDenied
	}
	return nil
}
func (g Grant) validatePaths() error {
	if !strings.HasPrefix(g.TaskRoot, "/data/tasks/") || !strings.HasPrefix(g.CWD, g.TaskRoot+"/") ||
		filepath.Clean(g.TaskRoot) != g.TaskRoot || filepath.Clean(g.CWD) != g.CWD {
		return ErrDenied
	}
	rootHash := sha256.Sum256([]byte(g.TaskRoot))
	projectHash := sha256.Sum256([]byte(g.Project))
	filename := hex.EncodeToString(projectHash[:]) + ".json"
	registry := filepath.Dir(filepath.Dir(g.ModelPath))
	if !filepath.IsAbs(g.ModelPath) || filepath.Base(registry) != "agent-docker-models" ||
		filepath.Base(filepath.Dir(g.ModelPath)) != hex.EncodeToString(rootHash[:]) || filepath.Base(g.ModelPath) != filename ||
		g.ClaimPath != filepath.Join(registry, "projects", filename) {
		return ErrDenied
	}
	return nil
}

func privateListener(address, publicPort string) bool {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if publicPort == "" {
		publicPort = "443"
	}
	number, portErr := strconv.Atoi(port)
	return err == nil && portErr == nil && number >= 1024 && number <= 65535 && port == publicPort && ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

func origin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "443" || u.Hostname() != strings.ToLower(u.Hostname()) {
		return nil, ErrDenied
	}
	return u, nil
}
func (g Grant) Active(now time.Time) bool {
	return now.Unix() >= g.IssuedAt && now.Unix() < g.ExpiresAt
}
func (g Grant) Digest() string {
	b, _ := json.Marshal(g)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type Claims struct {
	Grant   string `json:"grant"`
	Binding string `json:"binding"`
	User    string `json:"user"`
	Nonce   string `json:"nonce"`
	Issued  int64  `json:"issued"`
	Expires int64  `json:"expires"`
}

func newClaims(g Grant, now time.Time, ttl time.Duration) Claims {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic(err)
	}
	exp := now.Add(ttl).Unix()
	if exp > g.ExpiresAt {
		exp = g.ExpiresAt
	}
	return Claims{g.ID, g.Digest(), g.UserID, hex.EncodeToString(nonce[:]), now.Unix(), exp}
}
func sign(key []byte, purpose string, c Claims) string {
	b, _ := json.Marshal(c)
	raw := base64.RawURLEncoding.EncodeToString(b)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(purpose + "." + raw))
	return raw + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func verify(key []byte, purpose, token string, g Grant, now time.Time) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 2048 {
		return c, ErrDenied
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(purpose + "." + parts[0]))
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac, h.Sum(nil)) {
		return c, ErrDenied
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(b, &c) != nil || !validClaims(c, g, now) {
		return c, ErrDenied
	}
	return c, nil
}

func validClaims(c Claims, g Grant, now time.Time) bool {
	return noncePattern.MatchString(c.Nonce) && c.Grant == g.ID && c.Binding == g.Digest() && c.User == g.UserID &&
		c.Issued <= now.Unix() && c.Issued >= g.IssuedAt && c.Expires > now.Unix() && c.Expires <= g.ExpiresAt && g.Active(now)
}
