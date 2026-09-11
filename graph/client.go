package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
// returning one, accumulating every page's items.
func fetchAllPages[T any](ctx context.Context, c *Client, path string) ([]T, error) {
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
		next = page.NextLink
	}
	return all, nil
}

type Team struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (c *Client) ListJoinedTeams(ctx context.Context) ([]Team, error) {
	return fetchAllPages[Team](ctx, c, "/me/joinedTeams")
}

type Channel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (c *Client) ListChannels(ctx context.Context, teamID string) ([]Channel, error) {
	path := fmt.Sprintf("/teams/%s/channels", url.PathEscape(teamID))
	return fetchAllPages[Channel](ctx, c, path)
}

type Message struct {
	ID   string `json:"id"`
	From struct {
		User struct {
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"from"`
	CreatedDateTime string `json:"createdDateTime"`
	Body            struct {
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

func (c *Client) ReadChannelMessages(ctx context.Context, teamID, channelID string) ([]Message, error) {
	path := fmt.Sprintf("/teams/%s/channels/%s/messages", url.PathEscape(teamID), url.PathEscape(channelID))
	return fetchAllPages[Message](ctx, c, path)
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
	return fetchAllPages[Chat](ctx, c, "/me/chats?$expand=members")
}

func (c *Client) ReadChatMessages(ctx context.Context, chatID string) ([]Message, error) {
	path := fmt.Sprintf("/chats/%s/messages", url.PathEscape(chatID))
	return fetchAllPages[Message](ctx, c, path)
}

func (c *Client) SendChatMessage(ctx context.Context, chatID, text string) error {
	path := fmt.Sprintf("/chats/%s/messages", url.PathEscape(chatID))
	body := map[string]any{
		"body": map[string]string{"content": text},
	}
	_, err := c.do(ctx, http.MethodPost, baseURL+path, body)
	return err
}
