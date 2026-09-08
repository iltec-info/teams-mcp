package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"

	"teams-mcp/internal/config"
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

// diskCache persists the MSAL token cache to a file so the device-code
// flow only has to run once; subsequent runs refresh silently.
type diskCache struct {
	path string
}

func (d *diskCache) Export(_ context.Context, c cache.Marshaler, _ cache.ExportHints) error {
	data, err := c.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(d.path, data, 0600)
}

func (d *diskCache) Replace(_ context.Context, c cache.Unmarshaler, _ cache.ReplaceHints) error {
	data, err := os.ReadFile(d.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return c.Unmarshal(data)
}

func cachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "teams-mcp")
	if err := os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	return filepath.Join(d, "token_cache.json"), nil
}

// GetToken returns a valid Graph access token, refreshing silently from the
// disk cache when possible and falling back to an interactive device-code
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

	cp, err := cachePath()
	if err != nil {
		return "", err
	}

	app, err := public.New(clientID, public.WithAuthority(authority), public.WithCache(&diskCache{path: cp}))
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
