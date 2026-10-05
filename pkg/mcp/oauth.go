package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	picoauth "github.com/sipeed/picoclaw/pkg/auth"
	"github.com/sipeed/picoclaw/pkg/config"
)

// OAuthLoginOptions controls the interactive part of MCP authentication.
type OAuthLoginOptions struct {
	NoBrowser bool
	Output    io.Writer
	// OpenBrowser is optional; nil uses the system browser.
	OpenBrowser func(string) error
}

func validateOAuthConfig(cfg config.MCPServerConfig) error {
	transport := config.EffectiveMCPTransportType(cfg)
	if transport != "http" && transport != "sse" {
		return errors.New("MCP OAuth requires an HTTP/SSE server")
	}
	if err := validateOAuthURL(cfg.URL); err != nil {
		return err
	}
	for key := range cfg.Headers {
		if strings.EqualFold(key, "Authorization") {
			return errors.New("MCP OAuth cannot be combined with an Authorization header")
		}
	}
	if cfg.OAuth == nil {
		return errors.New("MCP OAuth is not configured")
	}
	if cfg.OAuth.CallbackPort < 0 || cfg.OAuth.CallbackPort > 65535 {
		return errors.New("OAuth callback_port must be between 0 and 65535")
	}
	if cfg.OAuth.Issuer != "" {
		return validateOAuthURL(cfg.OAuth.Issuer)
	}
	return nil
}

// LoginOAuth performs an explicit browser login. Background MCP connections use
// only saved credentials and never launch a browser or wait for user input.
func LoginOAuth(ctx context.Context, name string, cfg config.MCPServerConfig, opts OAuthLoginOptions) error {
	if err := validateOAuthConfig(cfg); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.OAuth.CallbackPort)))
	if err != nil {
		return fmt.Errorf("listen for MCP OAuth callback: %w", err)
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/callback"
	store := newOAuthStore(name, cfg)
	var resource string
	authorized := false
	handlerConfig := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:         redirect,
		RequestRefreshToken: true,
		Client: &http.Client{
			Timeout:       30 * time.Second,
			Transport:     oauthHTTPTransport{},
			CheckRedirect: rejectOAuthRedirect,
		},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			parsed, parseErr := url.Parse(args.URL)
			if parseErr != nil {
				return nil, errors.New("invalid authorization URL")
			}
			resource = parsed.Query().Get("resource")
			return fetchBrowserCode(ctx, listener, args.URL, opts)
		},
		NewTokenSource: func(_ context.Context, oauthCfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			if token.AccessToken == "" {
				return nil, errors.New("authorization server returned an empty access token")
			}
			defer store.lock()()
			if saveErr := store.save(
				&oauthSession{Config: oauthCfg, Token: token, Resource: resource},
			); saveErr != nil {
				return nil, saveErr
			}
			authorized = true
			return oauth2.StaticTokenSource(token), nil
		},
	}
	if cfg.OAuth.ClientID != "" {
		handlerConfig.PreregisteredClient = &oauthex.ClientCredentials{
			ClientID: cfg.OAuth.ClientID,
			Issuer:   cfg.OAuth.Issuer,
		}
	} else {
		handlerConfig.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName: "PicoClaw", RedirectURIs: []string{redirect}, TokenEndpointAuthMethod: "none",
				GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
			},
		}
	}
	if len(cfg.OAuth.Scopes) > 0 {
		handlerConfig.ScopeFilter = func(_ []string) []string { return append([]string(nil), cfg.OAuth.Scopes...) }
	}
	handler, err := auth.NewAuthorizationCodeHandler(handlerConfig)
	if err != nil {
		return err
	}
	conn, err := connectServerWithOAuth(ctx, name, cfg, handler)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// SDK/provider errors can contain token endpoint bodies. Keep them out of logs.
		return errors.New("MCP OAuth login failed; check client registration, server metadata and consent")
	}
	defer conn.Session.Close()
	if !authorized {
		return errors.New("MCP server did not request OAuth authorization")
	}
	return nil
}

func fetchBrowserCode(
	ctx context.Context,
	listener net.Listener,
	authURL string,
	opts OAuthLoginOptions,
) (*auth.AuthorizationResult, error) {
	if err := validateOAuthURL(authURL); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(authURL)
	state := parsed.Query().Get("state")
	if state == "" {
		return nil, errors.New("OAuth authorization state is missing")
	}
	type result struct {
		auth *auth.AuthorizationResult
		err  error
	}
	results := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		q := r.URL.Query()
		if r.Method != http.MethodGet || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid OAuth callback", http.StatusBadRequest)
			return
		}
		res := result{}
		if q.Get("error") != "" {
			res.err = errors.New("OAuth authorization was denied")
		} else if q.Get("code") == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			return
		} else {
			res.auth = &auth.AuthorizationResult{Code: q.Get("code"), State: state, Iss: q.Get("iss")}
		}
		select {
		case results <- res:
		default:
		}
		fmt.Fprintln(w, "Authorization response received. You can close this window.")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	output := opts.Output
	if output == nil {
		output = io.Discard
	}
	fmt.Fprintf(output, "Open this URL to authorize MCP access:\n%s\n", authURL)
	if !opts.NoBrowser {
		open := opts.OpenBrowser
		if open == nil {
			open = picoauth.OpenBrowser
		}
		if err := open(authURL); err != nil {
			fmt.Fprintln(output, "Could not open the browser automatically; open the URL above manually.")
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-serveErr:
		return nil, fmt.Errorf("OAuth callback server: %w", err)
	case res := <-results:
		return res.auth, res.err
	}
}
