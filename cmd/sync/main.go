package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"teams-mcp/audit"
	"teams-mcp/authapp"
	"teams-mcp/graph"
	"teams-mcp/internal/config"
)

// Fixed targets: the Teams digest channel this was built for, and the
// Trello board/list where extracted tasks land. From internal/config
// (gitignored config.go) rather than env/flags, both of which proved
// unreliable to pass through the MCP host that launches teams-mcp.exe.
const (
	teamID    = config.TeamID
	channelID = config.ChannelID

	trelloBoard  = config.TrelloBoardID
	trelloListID = config.TrelloListID
)

// Trello credentials are read from a local file rather than env vars:
// Task Scheduler (a long-running service) caches a stale user-env snapshot
// from logon time and doesn't reliably forward newly-set env vars to
// triggered tasks, the same class of bug hit with the MCP host's env.
var trelloKey, trelloToken = readTrelloCreds()

func readTrelloCreds() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".trello_creds"))
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return "", ""
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
}

// Assignee to filter tasks by, as the two @mention tag texts Teams renders
// side by side (first name, last name — see mentionRe).
const assigneeFirst, assigneeLast = config.AssigneeFirstName, config.AssigneeLastName

// NOTE: this is a regex scrape of one bot's specific Markdown-in-HTML digest
// shape ("- [ ] task — <at>First</at><at>Last</at><br>"). It is inherently
// fragile: any change to the digest bot's formatting, or a differently
// spelled/ordered mention, silently drops tasks rather than erroring. A
// robust fix would need a real HTML parser plus fuzzy name matching against
// Graph's structured `mentions` array (already available on graph.Message)
// instead of scraping Body.Content — out of scope for this pass.
var (
	taskLineRe = regexp.MustCompile(`(?s)\[\s*\]\s*(.+?)<br>`)
	mentionRe  = regexp.MustCompile(fmt.Sprintf(`<at[^>]*>%s</at>\s*<at[^>]*>%s</at>`, assigneeFirst, assigneeLast))
	tagRe      = regexp.MustCompile(`<[^>]+>`)
	dashRe     = regexp.MustCompile(`[—–-]`)
)

func extractTaskText(raw string) string {
	raw = strings.ReplaceAll(raw, "​", "")
	raw = strings.ReplaceAll(raw, "&nbsp;", " ")
	idx := strings.Index(raw, "<at")
	if idx == -1 {
		return ""
	}
	head := raw[:idx]
	if locs := dashRe.FindAllStringIndex(head, -1); len(locs) > 0 {
		last := locs[len(locs)-1]
		if last[0] > len(head)-30 {
			head = head[:last[0]]
		}
	}
	head = tagRe.ReplaceAllString(head, "")
	return strings.TrimSpace(head)
}

func extractTasks(messages []graph.Message) []string {
	seen := map[string]bool{}
	var tasks []string
	for _, m := range messages {
		for _, blockMatch := range taskLineRe.FindAllStringSubmatch(m.Body.Content, -1) {
			raw := strings.ReplaceAll(blockMatch[1], "&nbsp;", " ")
			if !mentionRe.MatchString(raw) {
				continue
			}
			t := extractTaskText(raw)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func trelloGet(path string, extra url.Values) ([]byte, error) {
	q := extra
	if q == nil {
		q = url.Values{}
	}
	q.Set("key", trelloKey)
	q.Set("token", trelloToken)
	u := fmt.Sprintf("https://api.trello.com/1%s?%s", path, q.Encode())
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("trello GET %s: %s: %s", path, resp.Status, string(body))
	}
	return body, nil
}

func existingCardNames() (map[string]bool, error) {
	data, err := trelloGet(fmt.Sprintf("/boards/%s/cards", trelloBoard), url.Values{"fields": {"name"}})
	if err != nil {
		return nil, err
	}
	var cards []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("unmarshal cards: %w (%s)", err, string(data))
	}
	names := map[string]bool{}
	for _, c := range cards {
		names[c.Name] = true
	}
	return names, nil
}

func createCard(name string) error {
	form := url.Values{
		"key":    {trelloKey},
		"token":  {trelloToken},
		"idList": {trelloListID},
		"name":   {name},
		"desc":   {fmt.Sprintf("Auto-synced from the configured Teams digest channel (assigned to %s %s).", assigneeFirst, assigneeLast)},
	}
	resp, err := http.Post("https://api.trello.com/1/cards?"+form.Encode(), "", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trello create card %q: %s: %s", name, resp.Status, string(body))
	}
	return nil
}

func main() {
	if trelloKey == "" || trelloToken == "" {
		log.Fatal("~/.trello_creds must exist with key on line 1, token on line 2")
	}
	principal, err := authapp.ClientID()
	if err != nil {
		log.Fatalf("service identity: %v", err)
	}

	ctx := context.Background()
	token, err := authapp.GetToken(ctx)
	if err != nil {
		log.Fatalf("app-only auth: %v", err)
	}
	client := graph.NewClient(func(context.Context) (string, error) { return token, nil })

	messages, err := client.ReadChannelMessages(ctx, teamID, channelID)
	audit.Log(principal, "graph.read_channel", teamID+"/"+channelID, err)
	if err != nil {
		log.Fatalf("read channel: %v", err)
	}

	tasks := extractTasks(messages)
	existing, err := existingCardNames()
	audit.Log(principal, "trello.list_cards", trelloBoard, err)
	if err != nil {
		log.Fatalf("fetch existing trello cards: %v", err)
	}

	created := 0
	for _, t := range tasks {
		if existing[t] {
			continue
		}
		err := createCard(t)
		audit.Log(principal, "trello.create_card", t, err)
		if err != nil {
			log.Printf("create card failed: %v", err)
			continue
		}
		created++
		fmt.Printf("created: %s\n", t)
	}
	fmt.Printf("done: %d tasks found, %d new cards created, %d already on board\n", len(tasks), created, len(tasks)-created)
}
