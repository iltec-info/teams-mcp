// Package secrets keeps credentials in the operating system's secret store
// (Windows Credential Manager / macOS Keychain / Linux Secret Service)
// instead of files. On Windows the store encrypts per-user with DPAPI, so
// the data is unreadable to other accounts and to anything copying the disk.
//
// Credential Manager caps one entry at ~2.5 KB and the MSAL token cache is
// larger, so values are split into chunks: <name> holds the chunk count and
// <name>#0..N-1 hold the pieces.
package secrets

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "teams-mcp"

// Entry names.
const (
	TrelloKey   = "trello-key"
	TrelloToken = "trello-token"
	MSALCache   = "msal-token-cache"
)

// chunkRunes stays well under the Windows limit (2560 bytes, stored as
// UTF-16) and never splits a multi-byte character.
const chunkRunes = 800

var ErrNotFound = keyring.ErrNotFound

func chunkName(name string, i int) string { return fmt.Sprintf("%s#%d", name, i) }

func count(name string) (int, error) {
	s, err := keyring.Get(service, name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("secret %q: corrupt chunk header", name)
	}
	return n, nil
}

// Get returns the stored value, or ErrNotFound.
func Get(name string) (string, error) {
	n, err := count(name)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		part, err := keyring.Get(service, chunkName(name, i))
		if err != nil {
			return "", fmt.Errorf("secret %q chunk %d: %w", name, i, err)
		}
		b.WriteString(part)
	}
	return b.String(), nil
}

// Set stores value, replacing any previous one (stale chunks are removed).
func Set(name, value string) error {
	old, _ := count(name)
	runes := []rune(value)
	n := 0
	for start := 0; start < len(runes); start += chunkRunes {
		end := min(start+chunkRunes, len(runes))
		if err := keyring.Set(service, chunkName(name, n), string(runes[start:end])); err != nil {
			return err
		}
		n++
	}
	if err := keyring.Set(service, name, strconv.Itoa(n)); err != nil {
		return err
	}
	for i := n; i < old; i++ {
		_ = keyring.Delete(service, chunkName(name, i))
	}
	return nil
}

// Delete removes the value; a missing entry is not an error.
func Delete(name string) error {
	n, err := count(name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	for i := 0; i < n; i++ {
		_ = keyring.Delete(service, chunkName(name, i))
	}
	err = keyring.Delete(service, name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Size reports the stored length in bytes without exposing the value.
func Size(name string) (int, error) {
	v, err := Get(name)
	return len(v), err
}
