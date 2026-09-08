// Package authapp implements app-only (client credentials) auth for the
// unattended scheduled sync job, as opposed to auth.GetToken's interactive
// device-code flow used by the MCP server for Claude Code sessions.
//
// The private key backing this identity is a non-exportable RSA key
// generated directly inside the current user's Windows CNG certificate
// store (see cmd/provision-cert) — it never exists as a file and is never
// extractable, even by an admin reading disk. Every token acquisition
// signs a fresh JWT client assertion through that CNG key via
// certtostore's crypto.Signer implementation; the raw key material never
// leaves the OS.
package authapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
	"github.com/google/certtostore"
	"github.com/google/uuid"

	"teams-mcp/internal/config"
)

const (
	certProvider      = "Microsoft Software Key Storage Provider"
	certContainer     = "teams-trello-sync-key"
	certThumbprintX5T = config.ServiceCertThumbprintX5T // base64url SHA1, from cmd/provision-cert output
)

var graphDefaultScope = []string{"https://graph.microsoft.com/.default"}

// serviceIdentity holds the Entra app registration's client ID and tenant
// ID for this service principal — read from a local file (not env/args;
// both have proven unreliable to pass through the process spawn paths
// this binary runs under). One line each: clientID, tenantID.
func serviceIdentity() (clientID, tenantID string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(filepath.Join(home, ".teams_service_identity"))
	if err != nil {
		return "", "", fmt.Errorf("reading ~/.teams_service_identity (clientID line 1, tenantID line 2): %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return "", "", fmt.Errorf("~/.teams_service_identity must have clientID on line 1, tenantID on line 2")
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}

// signAssertion builds and signs the JWT client assertion Azure AD requires
// for certificate-credential client-credentials auth, signing through the
// CNG-backed crypto.Signer so the private key never leaves the OS.
func signAssertion(signer crypto.Signer, clientID, tenantID string) (string, error) {
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"x5t": certThumbprintX5T,
	}
	now := time.Now()
	claims := map[string]any{
		"aud": fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		"iss": clientID,
		"sub": clientID,
		"jti": uuid.NewString(),
		"nbf": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("signing client assertion via CNG: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ClientID returns the configured service principal's client ID, for use
// as the audit-log principal — an auditable app identity rather than a
// human's cached login.
func ClientID() (string, error) {
	clientID, _, err := serviceIdentity()
	return clientID, err
}

// GetToken acquires a Graph app-only access token via client-credentials
// flow, signing the client assertion through the non-exportable CNG key
// on every call (client credential tokens are short-lived and this job
// runs once a day, so no persistent token cache is needed).
func GetToken(ctx context.Context) (string, error) {
	clientID, tenantID, err := serviceIdentity()
	if err != nil {
		return "", err
	}

	store, err := certtostore.OpenWinCertStoreCurrentUser(certProvider, certContainer, nil, nil, false)
	if err != nil {
		return "", fmt.Errorf("open CNG cert store: %w", err)
	}
	defer store.Close()

	cred, err := store.Key()
	if err != nil {
		return "", fmt.Errorf("open non-exportable key %q: %w", certContainer, err)
	}
	signer, ok := cred.(crypto.Signer)
	if !ok {
		return "", fmt.Errorf("CNG key does not implement crypto.Signer")
	}

	assertionCred := confidential.NewCredFromAssertionCallback(
		func(ctx context.Context, _ confidential.AssertionRequestOptions) (string, error) {
			return signAssertion(signer, clientID, tenantID)
		},
	)

	authority := fmt.Sprintf("https://login.microsoftonline.com/%s", tenantID)
	client, err := confidential.New(authority, clientID, assertionCred)
	if err != nil {
		return "", fmt.Errorf("build confidential client: %w", err)
	}

	result, err := client.AcquireTokenByCredential(ctx, graphDefaultScope)
	if err != nil {
		return "", fmt.Errorf("acquire app-only token: %w", err)
	}
	return result.AccessToken, nil
}
