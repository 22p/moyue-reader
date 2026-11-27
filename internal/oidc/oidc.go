// Package oidc 实现 OpenID Connect 授权码登录（带 PKCE）。
//
// 只依赖发现文档与 JWKS，不绑定特定身份提供商；本地不保存密码，
// 管理员范围只由配置中的 OIDC subject 白名单决定。
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const discoveryLimit = 1 << 20

// Config 是 OIDC 登录的启动配置。
type Config struct {
	// DiscoveryURL 是完整的发现文档地址，例如
	// https://id.example.com/.well-known/openid-configuration
	DiscoveryURL string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// AdminIDs 是管理员 OIDC subject 白名单，按大小写精确匹配。
	AdminIDs []string
}

// Enabled 表示是否配置了 OIDC 登录。
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.DiscoveryURL) != ""
}

// Discovery 是发现文档中本服务需要的字段。
type Discovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	EndSessionEndpoint    string   `json:"end_session_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
	SigningAlgs           []string `json:"id_token_signing_alg_values_supported"`
}

// Identity 是校验通过后的登录身份。
type Identity struct {
	Issuer            string
	Subject           string
	Email             string
	DisplayName       string
	PreferredUsername string
	AvatarURL         string
	Nonce             string
	IDToken           string
}

// Client 持有发现文档、JWKS 与 OAuth2 配置，可并发使用。
type Client struct {
	config        Config
	discovery     Discovery
	verifier      *coreoidc.IDTokenVerifier
	oauth2        oauth2.Config
	adminSubjects map[string]struct{}
	httpClient    *http.Client
}

// New 拉取发现文档并初始化校验器。调用失败通常意味着地址或网络配置有误。
func New(ctx context.Context, cfg Config) (*Client, error) {
	cfg.DiscoveryURL = strings.TrimSpace(cfg.DiscoveryURL)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	if cfg.DiscoveryURL == "" {
		return nil, errors.New("OIDC discovery URL is required")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("OIDC client ID is required")
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.DiscoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build OIDC discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch OIDC discovery document: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch OIDC discovery document: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var discovery Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, discoveryLimit)).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("parse OIDC discovery document: %w", err)
	}
	if discovery.Issuer == "" || discovery.AuthorizationEndpoint == "" ||
		discovery.TokenEndpoint == "" || discovery.JWKSURI == "" {
		return nil, errors.New("OIDC discovery document is missing issuer, authorization_endpoint, token_endpoint or jwks_uri")
	}

	keysetCtx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	keySet := coreoidc.NewRemoteKeySet(keysetCtx, discovery.JWKSURI)
	verifyConfig := &coreoidc.Config{ClientID: cfg.ClientID}
	if len(discovery.SigningAlgs) > 0 {
		verifyConfig.SupportedSigningAlgs = discovery.SigningAlgs
	}
	verifier := coreoidc.NewVerifier(discovery.Issuer, keySet, verifyConfig)

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{coreoidc.ScopeOpenID, "profile", "email"}
	}
	adminSubjects := make(map[string]struct{}, len(cfg.AdminIDs))
	for _, id := range cfg.AdminIDs {
		if id = strings.TrimSpace(id); id != "" {
			adminSubjects[id] = struct{}{}
		}
	}

	return &Client{
		config:    cfg,
		discovery: discovery,
		verifier:  verifier,
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  discovery.AuthorizationEndpoint,
				TokenURL: discovery.TokenEndpoint,
			},
			Scopes: scopes,
		},
		adminSubjects: adminSubjects,
		httpClient:    httpClient,
	}, nil
}

// EndSessionEndpoint 返回身份提供商的注销端点，可能为空。
func (c *Client) EndSessionEndpoint() string { return c.discovery.EndSessionEndpoint }

// LogoutURL 构造 RP 主动注销地址；不支持注销端点时返回空字符串。
func (c *Client) LogoutURL(postLogoutRedirect, idToken string) string {
	endpoint := strings.TrimSpace(c.discovery.EndSessionEndpoint)
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("client_id", c.config.ClientID)
	if postLogoutRedirect != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirect)
	}
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// IsAdminSubject 判断 OIDC subject 是否在白名单中（区分大小写）。
func (c *Client) IsAdminSubject(subject string) bool {
	if subject == "" {
		return false
	}
	_, ok := c.adminSubjects[subject]
	return ok
}

// AuthCodeURL 生成授权跳转地址，并附带 nonce 与 PKCE S256 挑战。
// prompt 非空时作为 prompt 参数传给身份提供商（none 表示静默检查现有会话）。
func (c *Client) AuthCodeURL(redirectURL, state, nonce, codeVerifier, prompt string) string {
	opts := []oauth2.AuthCodeOption{
		coreoidc.Nonce(nonce),
		oauth2.S256ChallengeOption(codeVerifier),
	}
	if prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	return c.configFor(redirectURL).AuthCodeURL(state, opts...)
}

func (c *Client) configFor(redirectURL string) *oauth2.Config {
	// 每次构造新配置，避免复制 oauth2.Config 内部的缓存字段。
	return &oauth2.Config{
		ClientID:     c.oauth2.ClientID,
		ClientSecret: c.oauth2.ClientSecret,
		Endpoint:     c.oauth2.Endpoint,
		RedirectURL:  redirectURL,
		Scopes:       c.oauth2.Scopes,
	}
}

// Exchange 用授权码换取并校验 ID Token。
func (c *Client) Exchange(ctx context.Context, redirectURL, code, codeVerifier string) (*Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	token, err := c.configFor(redirectURL).Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, errors.New("token response is missing id_token")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify ID token: %w", err)
	}
	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Picture           string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse ID token claims: %w", err)
	}
	return &Identity{
		Issuer:            idToken.Issuer,
		Subject:           idToken.Subject,
		Email:             claims.Email,
		DisplayName:       claims.Name,
		PreferredUsername: claims.PreferredUsername,
		AvatarURL:         claims.Picture,
		Nonce:             idToken.Nonce,
		IDToken:           rawIDToken,
	}, nil
}
