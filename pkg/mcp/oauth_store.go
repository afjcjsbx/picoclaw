package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Sessions are isolated by server name, endpoint and OAuth configuration. A
// changed endpoint or client must never inherit another resource's credentials.
type (
	oauthStore   struct{ path string }
	oauthSession struct {
		Config   *oauth2.Config `json:"config"`
		Token    *oauth2.Token  `json:"token"`
		Resource string         `json:"resource"`
	}
)

var oauthStoreLocks sync.Map

func (s oauthStore) lock() func() {
	value, _ := oauthStoreLocks.LoadOrStore(s.path, new(sync.Mutex))
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func newOAuthStore(name string, cfg config.MCPServerConfig) oauthStore {
	identity, _ := json.Marshal(struct {
		Name  string                 `json:"name"`
		URL   string                 `json:"url"`
		OAuth *config.MCPOAuthConfig `json:"oauth"`
	}{name, cfg.URL, cfg.OAuth})
	key := sha256.Sum256(identity)
	return oauthStore{filepath.Join(config.GetHome(), "auth", "mcp", fmt.Sprintf("%x.json", key))}
}

func (s oauthStore) load() (*oauthSession, error) {
	info, err := os.Lstat(s.path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("MCP OAuth token file must be a regular file with owner-only permissions")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var session oauthSession
	if json.Unmarshal(data, &session) != nil || session.Config == nil || session.Token == nil ||
		session.Token.AccessToken == "" {
		return nil, errors.New("invalid MCP OAuth token file; log in again")
	}
	if validateOAuthURL(session.Config.Endpoint.TokenURL) != nil || session.Resource == "" {
		return nil, errors.New("invalid MCP OAuth session endpoints; log in again")
	}
	return &session, nil
}

func (s oauthStore) save(session *oauthSession) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid MCP OAuth token directory")
	}
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil {
		return chmodErr
	}
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}

// LogoutOAuth removes local credentials. Already issued server-side tokens are
// not revoked. Running clients read the store again before their next request.
func LogoutOAuth(name string, cfg config.MCPServerConfig) error {
	store := newOAuthStore(name, cfg)
	defer store.lock()()
	err := os.Remove(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type storedOAuthHandler struct {
	name  string
	store oauthStore
}

func (h *storedOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return &storedTokenSource{ctx: ctx, name: h.name, store: h.store}, nil
}

func (h *storedOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	resp.Body.Close()
	return loginRequired(h.name)
}

func loginRequired(name string) error {
	return fmt.Errorf("MCP OAuth authorization required; run picoclaw mcp login %q", name)
}

type storedTokenSource struct {
	ctx   context.Context
	name  string
	store oauthStore
}

func (s *storedTokenSource) Token() (*oauth2.Token, error) {
	// Reload while holding a shared lock so parallel connections in this process
	// cannot refresh the same rotating refresh token twice, or undo logout.
	defer s.store.lock()()
	session, err := s.store.load()
	if errors.Is(err, os.ErrNotExist) {
		return nil, loginRequired(s.name)
	}
	if err != nil {
		return nil, err
	}
	if session.Token.Valid() {
		return session.Token, nil
	}
	client := &http.Client{
		Timeout:       30 * time.Second,
		Transport:     oauthHTTPTransport{tokenURL: session.Config.Endpoint.TokenURL, resource: session.Resource},
		CheckRedirect: rejectOAuthRedirect,
	}
	ctx := context.WithValue(s.ctx, oauth2.HTTPClient, client)
	token, err := session.Config.TokenSource(ctx, session.Token).Token()
	if err != nil {
		// Provider errors can include response bodies containing credentials.
		if s.ctx.Err() != nil {
			return nil, s.ctx.Err()
		}
		return nil, fmt.Errorf("MCP OAuth token refresh failed; %w", loginRequired(s.name))
	}
	session.Token = token
	if err := s.store.save(session); err != nil {
		return nil, fmt.Errorf("save refreshed MCP OAuth token: %w", err)
	}
	return token, nil
}

// Do not follow redirects carrying tokens, custom headers or refresh bodies.
func rejectOAuthRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func validateOAuthURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid OAuth URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" &&
		(u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || strings.EqualFold(u.Hostname(), "localhost")) {
		return nil
	}
	return errors.New("OAuth requires HTTPS (HTTP is allowed only on loopback)")
}

type oauthHTTPTransport struct{ tokenURL, resource string }

func (t oauthHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateOAuthURL(req.URL.String()); err != nil {
		return nil, err
	}
	if t.resource != "" && req.URL.String() == t.tokenURL && req.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		values.Set("resource", t.resource)
		req = req.Clone(req.Context())
		encoded := values.Encode()
		req.Body = io.NopCloser(strings.NewReader(encoded))
		req.ContentLength = int64(len(encoded))
	}
	return http.DefaultTransport.RoundTrip(req)
}
