// Package trello is a thin Trello REST v1 client plus the MCP tool surface
// built on it. Credentials come from ~/.trello_creds (API key on line 1,
// token on line 2) — the same file cmd/sync uses — and are sent in the
// Authorization header, never in the URL, so they cannot leak into error
// strings or logs.
package trello

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const baseURL = "https://api.trello.com/1"

type Client struct {
	key, token string
	http       *http.Client
}

func NewClient(key, token string) *Client {
	return &Client{key: key, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// NewClientFromFile reads ~/.trello_creds (key on line 1, token on line 2).
func NewClientFromFile() (*Client, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".trello_creds"))
	if err != nil {
		return nil, fmt.Errorf("read ~/.trello_creds: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[1]) == "" {
		return nil, errors.New("~/.trello_creds must contain the API key on line 1 and the token on line 2")
	}
	return NewClient(strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])), nil
}

// do performs one request. Parameters travel in the query string (Trello
// accepts that for POST/PUT too); credentials travel in the header.
func (c *Client) do(ctx context.Context, method, path string, q url.Values) ([]byte, error) {
	u := baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("trello %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", fmt.Sprintf(`OAuth oauth_consumer_key="%s", oauth_token="%s"`, c.key, c.token))
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL from the message
		}
		return nil, fmt.Errorf("trello %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("trello %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("trello %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	b, err := c.do(ctx, http.MethodGet, path, q)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (c *Client) send(ctx context.Context, method, path string, q url.Values, out any) error {
	b, err := c.do(ctx, method, path, q)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

type Board struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Closed bool   `json:"closed"`
	URL    string `json:"url"`
}

type List struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Closed bool   `json:"closed"`
}

type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type CheckItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"` // "complete" | "incomplete"
}

type Checklist struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	CheckItems []CheckItem `json:"checkItems"`
}

type Card struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Desc        string      `json:"desc"`
	IDList      string      `json:"idList"`
	Closed      bool        `json:"closed"`
	Due         string      `json:"due"`
	DueComplete bool        `json:"dueComplete"`
	ShortURL    string      `json:"shortUrl"`
	Labels      []Label     `json:"labels"`
	Checklists  []Checklist `json:"checklists"`
}

type Comment struct {
	ID   string `json:"id"`
	Date string `json:"date"`
	Data struct {
		Text string `json:"text"`
	} `json:"data"`
	MemberCreator struct {
		FullName string `json:"fullName"`
	} `json:"memberCreator"`
}

func (c *Client) ListBoards(ctx context.Context) ([]Board, error) {
	var out []Board
	err := c.get(ctx, "/members/me/boards", url.Values{"fields": {"name,closed,url"}, "filter": {"open"}}, &out)
	return out, err
}

func (c *Client) ListLists(ctx context.Context, boardID string) ([]List, error) {
	var out []List
	err := c.get(ctx, "/boards/"+url.PathEscape(boardID)+"/lists", url.Values{"fields": {"name,closed"}, "filter": {"open"}}, &out)
	return out, err
}

func (c *Client) ListLabels(ctx context.Context, boardID string) ([]Label, error) {
	var out []Label
	err := c.get(ctx, "/boards/"+url.PathEscape(boardID)+"/labels", url.Values{"fields": {"name,color"}, "limit": {"1000"}}, &out)
	return out, err
}

var cardFields = "name,desc,idList,closed,due,dueComplete,shortUrl,labels"

// ListCardsByBoard filter: "open" | "closed" | "all".
func (c *Client) ListCardsByBoard(ctx context.Context, boardID, filter string) ([]Card, error) {
	var out []Card
	err := c.get(ctx, "/boards/"+url.PathEscape(boardID)+"/cards/"+url.PathEscape(filter), url.Values{"fields": {cardFields}}, &out)
	return out, err
}

func (c *Client) ListCardsByList(ctx context.Context, listID string) ([]Card, error) {
	var out []Card
	err := c.get(ctx, "/lists/"+url.PathEscape(listID)+"/cards", url.Values{"fields": {cardFields}}, &out)
	return out, err
}

func (c *Client) GetCard(ctx context.Context, cardID string) (*Card, error) {
	var out Card
	err := c.get(ctx, "/cards/"+url.PathEscape(cardID), url.Values{"fields": {cardFields}, "checklists": {"all"}}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListComments(ctx context.Context, cardID string, limit int) ([]Comment, error) {
	var out []Comment
	err := c.get(ctx, "/cards/"+url.PathEscape(cardID)+"/actions", url.Values{"filter": {"commentCard"}, "limit": {fmt.Sprint(limit)}}, &out)
	return out, err
}

func (c *Client) CreateCard(ctx context.Context, q url.Values) (*Card, error) {
	var out Card
	if err := c.send(ctx, http.MethodPost, "/cards", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateCard(ctx context.Context, cardID string, q url.Values) (*Card, error) {
	var out Card
	if err := c.send(ctx, http.MethodPut, "/cards/"+url.PathEscape(cardID), q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AddComment(ctx context.Context, cardID, text string) error {
	return c.send(ctx, http.MethodPost, "/cards/"+url.PathEscape(cardID)+"/actions/comments", url.Values{"text": {text}}, nil)
}

func (c *Client) CreateChecklist(ctx context.Context, cardID, name string) (*Checklist, error) {
	var out Checklist
	if err := c.send(ctx, http.MethodPost, "/checklists", url.Values{"idCard": {cardID}, "name": {name}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AddCheckItem(ctx context.Context, checklistID, name string) (*CheckItem, error) {
	var out CheckItem
	if err := c.send(ctx, http.MethodPost, "/checklists/"+url.PathEscape(checklistID)+"/checkItems", url.Values{"name": {name}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetCheckItemState state: "complete" | "incomplete".
func (c *Client) SetCheckItemState(ctx context.Context, cardID, itemID, state string) error {
	return c.send(ctx, http.MethodPut, "/cards/"+url.PathEscape(cardID)+"/checkItem/"+url.PathEscape(itemID), url.Values{"state": {state}}, nil)
}
