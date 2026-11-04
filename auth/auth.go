package auth

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"

	"teams-mcp/internal/config"
	"teams-mcp/secrets"
)

var scopes = []string{
	"Chat.Read",
	"ChannelMessage.Send",
	"ChannelMessage.Read.All",
	"Team.ReadBasic.All",
	"Channel.ReadBasic.All",
}

// Baked-in defaults: the MCP host that launches this binary does not
// reliably forward configured args or env vars to the child process, so
// falling back to flags/env alone left auth stuck on the wrong tenant.
const (
	defaultClientID = config.PublicClientID
	defaultTenantID = config.TenantID
)

// storeCache persists the MSAL token cache in the OS secret store so the
// device-code flow only has to run once; subsequent runs refresh silently.
// The cache holds a refresh token, so it never touches the filesystem.
type storeCache struct{}

func (storeCache) Export(_ context.Context, c cache.Marshaler, _ cache.ExportHints) error {
	data, err := c.Marshal()
	if err != nil {
		return err
	}
	return secrets.Set(secrets.MSALCache, string(data))
}

func (storeCache) Replace(_ context.Context, c cache.Unmarshaler, _ cache.ReplaceHints) error {
	data, err := secrets.Get(secrets.MSALCache)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil
		}
		return err
	}
	return c.Unmarshal([]byte(data))
}

// GetToken returns a valid Graph access token, refreshing silently from the
// secret-store cache when possible and falling back to an interactive device-code
// login (printed to stderr) on first run or after the refresh token expires.
// clientID/tenantID, when non-empty, override the baked-in defaults; there
// is no env var fallback — env vars proved unreliable across both the MCP
// host's pooled subprocess and Task Scheduler's cached logon environment.
func GetToken(ctx context.Context, clientID, tenantID string) (string, error) {
	if clientID == "" {
		clientID = defaultClientID
	}
	if tenantID == "" {
		tenantID = defaultTenantID
	}
	authority := fmt.Sprintf("https://login.microsoftonline.com/%s", tenantID)

	app, err := public.New(clientID, public.WithAuthority(authority), public.WithCache(storeCache{}))
	if err != nil {
		return "", err
	}

	if accounts, err := app.Accounts(ctx); err == nil {
		for _, acct := range accounts {
			result, err := app.AcquireTokenSilent(ctx, scopes, public.WithSilentAccount(acct))
			if err == nil {
				return result.AccessToken, nil
			}
		}
	}

	devCode, err := app.AcquireTokenByDeviceCode(ctx, scopes)
	if err != nil {
		return "", fmt.Errorf("starting device code flow: %w", err)
	}
	fmt.Fprintln(os.Stderr, devCode.Result.Message)

	result, err := devCode.AuthenticationResult(ctx)
	if err != nil {
		return "", fmt.Errorf("completing device code flow: %w", err)
	}
	return result.AccessToken, nil
}
