// Package oidc signs users in with an OpenID Connect provider (Authorization
// Code flow with PKCE). Google is the first one; others only need their
// issuer URL and credentials.
package oidc

import (
	"context"
	"errors"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider is one configured login provider.
type Provider struct {
	ID     string // short name, also the identities.provider value
	Name   string // shown on the button
	cfg    oauth2.Config
	verify *gooidc.IDTokenVerifier
}

// Claims is what tildoc takes from a verified ID token.
type Claims struct {
	Subject string
	Email   string
	Name    string
}

// New discovers the provider's endpoints and keys from its issuer URL.
func New(ctx context.Context, id, name, issuer, clientID, clientSecret, redirectURL string) (*Provider, error) {
	p, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc %s: %w", id, err)
	}
	return &Provider{ID: id, Name: name,
		cfg: oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
			Endpoint: p.Endpoint(), Scopes: []string{gooidc.ScopeOpenID, "email", "profile"}},
		verify: p.Verifier(&gooidc.Config{ClientID: clientID})}, nil
}

// NewVerifier returns a fresh PKCE verifier.
func NewVerifier() string { return oauth2.GenerateVerifier() }

// AuthURL is where the browser goes to sign in.
func (p *Provider) AuthURL(state, nonce, verifier string) string {
	return p.cfg.AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

// Exchange trades the code for tokens and verifies the ID token: signature,
// issuer, audience, expiry and nonce.
func (p *Provider) Exchange(ctx context.Context, code, verifier, nonce string) (*Claims, error) {
	tok, err := p.cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("oidc exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("oidc: no id_token in the response")
	}
	idt, err := p.verify.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("oidc verify: %w", err)
	}
	if idt.Nonce != nonce {
		return nil, errors.New("oidc: nonce mismatch")
	}
	var c struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idt.Claims(&c); err != nil {
		return nil, fmt.Errorf("oidc claims: %w", err)
	}
	return &Claims{Subject: idt.Subject, Email: c.Email, Name: c.Name}, nil
}
