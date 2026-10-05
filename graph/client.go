package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://graph.microsoft.com/v1.0"

// TokenFunc supplies a fresh bearer token for each request.
type TokenFunc func(ctx context.Context) (string, error)

type Client struct {
	http     *http.Client
	getToken TokenFunc
}

func NewClient(getToken TokenFunc) *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second}, getToken: getToken}
}

// do issues a request against a full URL (baseURL+path for a fresh request,
// or an @odata.nextLink verbatim for a pagination continuation).
func (c *Client) do(ctx context.Context, method, fullURL string, body any) ([]byte, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring token: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("graph API %s %s: %s: %s", method, fullURL, resp.Status, string(out))
	}
	return out, nil
}

type pagedResponse[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

// fetchAllPages GETs path and follows @odata.nextLink until Graph stops
// returning one, accumulating every page's items. When limit > 0 it stops
// paging as soon as limit items are collected and truncates to limit.
func fetchAllPages[T any](ctx context.Context, c *Client, path string, limit int) ([]T, error) {
	var all []T
	next := baseURL + path
	for next != "" {
		data, err := c.do(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		var page pagedResponse[T]
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Value...)
		if limit > 0 && len(all) >= limit {
			return all[:limit], nil
		}
		next = page.NextLink
	}
	return all, nil
}

type Team struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (c *Client) ListJoinedTeams(ctx context.Context) ([]Team, error) {
	return fetchAllPages[Team](ctx, c, "/me/joinedTeams", 0)
}

type Channel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (c *Client) ListChannels(ctx context.Context, teamID string) ([]Channel, error) {
	path := fmt.Sprintf("/teams/%s/channels", url.PathEscape(teamID))
	return fetchAllPages[Channel](ctx, c, path, 0)
}

type Message struct {
	ID   string `json:"id"`
	From struct {
		User struct {
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"from"`
	CreatedDateTime      string `json:"createdDateTime"`
	LastModifiedDateTime string `json:"lastModifiedDateTime"`
	Body                 struct {
		Content string `json:"content"`
	} `json:"body"`
	Mentions []struct {
		Mentioned struct {
			User struct {
				DisplayName string `json:"displayName"`
				ID          string `json:"id"`
			} `json:"user"`
		} `json:"mentioned"`
	} `json:"mentions"`
}

// MessageQuery narrows a message listing server-side. The zero value means
// "everything" and adds no query parameters, so unfiltered callers behave
// exactly as before.
type MessageQuery struct {
	Since time.Time // only messages created or edited after this instant; zero = no lower bound
	Limit int       // max messages returned, newest first; 0 = no cap
}

const maxGraphPageSize = 50

func (q MessageQuery) top() int {
	if q.Limit > 0 && q.Limit < maxGraphPageSize {
		return q.Limit
	}
	return maxGraphPageSize
}

// chatQuery renders the OData query string for chat message lists, which
// support $top (max 50), $orderby and $filter — but only on lastModifiedDateTime
// or createdDateTime, descending, and Graph ignores a $filter unless $orderby
// names the same property.
func (q MessageQuery) chatQuery() string {
	if q.Since.IsZero() && q.Limit <= 0 {
		return ""
	}
	parts := []string{
		"$orderby=" + escapeOData("lastModifiedDateTime desc"),
		fmt.Sprintf("$top=%d", q.top()),
	}
	if !q.Since.IsZero() {
		filter := "lastModifiedDateTime gt " + formatODataTime(q.Since)
		parts = append(parts, "$filter="+escapeOData(filter))
	}
	return strings.Join(parts, "&")
}

// channelQuery renders the OData query string for channel message lists, which
// support only $top and $expand — a $filter or $orderby there is not honoured,
// so ReadChannelMessages applies Since itself.
func (q MessageQuery) channelQuery() string {
	if q.Since.IsZero() && q.Limit <= 0 {
		return ""
	}
	return fmt.Sprintf("$top=%d", q.top())
}

func withQuery(path, queryString string) string {
	if queryString != "" {
		return path + "?" + queryString
	}
	return path
}

func formatODataTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func escapeOData(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// modifiedAt is when the message last changed, falling back to its creation
// time. ok is false when neither timestamp parses.
func (m Message) modifiedAt() (t time.Time, ok bool) {
	for _, s := range []string{m.LastModifiedDateTime, m.CreatedDateTime} {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// filterSince drops messages last modified at or before since. Messages with an
// unparseable timestamp are kept rather than silently discarded.
func filterSince(messages []Message, since time.Time) []Message {
	kept := make([]Message, 0, len(messages))
	for _, m := range messages {
		if t, ok := m.modifiedAt(); ok && !t.After(since) {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

func (c *Client) ReadChannelMessages(ctx context.Context, teamID, channelID string, q MessageQuery) ([]Message, error) {
	path := withQuery(fmt.Sprintf("/teams/%s/channels/%s/messages", url.PathEscape(teamID), url.PathEscape(channelID)), q.channelQuery())
	if q.Since.IsZero() {
		return fetchAllPages[Message](ctx, c, path, q.Limit)
	}

	// Every page has to be fetched before filtering: Graph orders channel
	// messages by the whole reply chain's last-modified time, not each
	// message's own, so an older message can still follow a newer one.
	all, err := fetchAllPages[Message](ctx, c, path, 0)
	if err != nil {
		return nil, err
	}
	kept := filterSince(all, q.Since)
	if q.Limit > 0 && len(kept) > q.Limit {
		kept = kept[:q.Limit]
	}
	return kept, nil
}

func (c *Client) SendChannelMessage(ctx context.Context, teamID, channelID, text string) error {
	path := fmt.Sprintf("/teams/%s/channels/%s/messages", url.PathEscape(teamID), url.PathEscape(channelID))
	body := map[string]any{
		"body": map[string]string{"content": text},
	}
	_, err := c.do(ctx, http.MethodPost, baseURL+path, body)
	return err
}

// Chat is a 1:1 or group chat (as opposed to a Team channel).
type Chat struct {
	ID         string `json:"id"`
	Topic      string `json:"topic"`
	ChatType   string `json:"chatType"` // "oneOnOne", "group", "meeting"
	Members    []struct {
		DisplayName string `json:"displayName"`
	} `json:"members"`
}

func (c *Client) ListChats(ctx context.Context) ([]Chat, error) {
	return fetchAllPages[Chat](ctx, c, "/me/chats?$expand=members", 0)
}

func (c *Client) ReadChatMessages(ctx context.Context, chatID string, q MessageQuery) ([]Message, error) {
	path := withQuery(fmt.Sprintf("/chats/%s/messages", url.PathEscape(chatID)), q.chatQuery())
	return fetchAllPages[Message](ctx, c, path, q.Limit)
}

func (c *Client) SendChatMessage(ctx context.Context, chatID, text string) error {
	path := fmt.Sprintf("/chats/%s/messages", url.PathEscape(chatID))
	body := map[string]any{
		"body": map[string]string{"content": text},
	}
	_, err := c.do(ctx, http.MethodPost, baseURL+path, body)
	return err
}
