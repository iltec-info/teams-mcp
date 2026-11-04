// credstore manages the credentials the Teams/Trello tools keep in the OS
// secret store (Windows Credential Manager) instead of files.
//
//	credstore status        which entries exist and their size (never the values)
//	credstore set-trello    read Trello API key (line 1) and token (line 2) from stdin
//	credstore import        one-time move of the legacy plaintext files
//	                        (~/.trello_creds, <UserConfigDir>/teams-mcp/token_cache.json)
//	                        into the store; each file is overwritten and deleted only
//	                        after the stored copy reads back identical
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"teams-mcp/secrets"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "status":
		status()
	case "set-trello":
		err = setTrello()
	case "import":
		err = importLegacy()
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: credstore status | set-trello | import")
	os.Exit(2)
}

func status() {
	for _, name := range []string{secrets.TrelloKey, secrets.TrelloToken, secrets.MSALCache} {
		n, err := secrets.Size(name)
		switch {
		case err == nil:
			fmt.Printf("%-18s stored (%d bytes)\n", name, n)
		case errors.Is(err, secrets.ErrNotFound):
			fmt.Printf("%-18s missing\n", name)
		default:
			fmt.Printf("%-18s error: %v\n", name, err)
		}
	}
}

func setTrello() error {
	sc := bufio.NewScanner(os.Stdin)
	var lines []string
	for sc.Scan() && len(lines) < 2 {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	if len(lines) < 2 || lines[0] == "" || lines[1] == "" {
		return errors.New("expected API key on line 1 and token on line 2 of stdin")
	}
	if err := store(secrets.TrelloKey, lines[0]); err != nil {
		return err
	}
	return store(secrets.TrelloToken, lines[1])
}

// store writes a value and confirms it reads back identical.
func store(name, value string) error {
	if err := secrets.Set(name, value); err != nil {
		return fmt.Errorf("store %s: %w", name, err)
	}
	got, err := secrets.Get(name)
	if err != nil || got != value {
		return fmt.Errorf("store %s: read-back mismatch (err=%v)", name, err)
	}
	fmt.Printf("stored %s (%d bytes)\n", name, len(value))
	return nil
}

func importLegacy() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	trelloFile := filepath.Join(home, ".trello_creds")
	if data, err := os.ReadFile(trelloFile); err == nil {
		lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
		if len(lines) < 2 || strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[1]) == "" {
			return fmt.Errorf("%s: expected key on line 1, token on line 2", trelloFile)
		}
		if err := store(secrets.TrelloKey, strings.TrimSpace(lines[0])); err != nil {
			return err
		}
		if err := store(secrets.TrelloToken, strings.TrimSpace(lines[1])); err != nil {
			return err
		}
		if err := shred(trelloFile); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	cacheFile := filepath.Join(cfg, "teams-mcp", "token_cache.json")
	if data, err := os.ReadFile(cacheFile); err == nil {
		if err := store(secrets.MSALCache, string(data)); err != nil {
			return err
		}
		if err := shred(cacheFile); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// shred overwrites the file with zeros, then deletes it. Best effort on
// journaling/SSD storage, but it removes the plaintext from the filesystem.
func shred(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		_, _ = f.Write(make([]byte, fi.Size()))
		_ = f.Sync()
		f.Close()
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Println("removed", path)
	return nil
}
