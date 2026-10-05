package graph

import (
	"testing"
	"time"
)

var since = time.Date(2026, 10, 5, 0, 0, 0, 0, time.FixedZone("EEST", 3*3600))

func TestChatQuery(t *testing.T) {
	cases := []struct {
		name string
		q    MessageQuery
		want string
	}{
		{"zero value adds nothing", MessageQuery{}, ""},
		{
			"since carries matching $orderby, else Graph ignores the $filter",
			MessageQuery{Since: since},
			"$orderby=lastModifiedDateTime%20desc&$top=50&$filter=lastModifiedDateTime%20gt%202026-10-04T21%3A00%3A00.000Z",
		},
		{"limit only", MessageQuery{Limit: 20}, "$orderby=lastModifiedDateTime%20desc&$top=20"},
		{"limit above max page size is capped", MessageQuery{Limit: 500}, "$orderby=lastModifiedDateTime%20desc&$top=50"},
	}
	for _, c := range cases {
		if got := c.q.chatQuery(); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

// Channel message lists support only $top and $expand, so neither $filter nor
// $orderby may appear there.
func TestChannelQueryOmitsFilterAndOrderby(t *testing.T) {
	cases := []struct {
		q    MessageQuery
		want string
	}{
		{MessageQuery{}, ""},
		{MessageQuery{Since: since}, "$top=50"},
		{MessageQuery{Limit: 10}, "$top=10"},
		{MessageQuery{Since: since, Limit: 10}, "$top=10"},
	}
	for _, c := range cases {
		if got := c.q.channelQuery(); got != c.want {
			t.Errorf("channelQuery(%+v) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestWithQuery(t *testing.T) {
	if got := withQuery("/chats/x/messages", ""); got != "/chats/x/messages" {
		t.Errorf("empty query changed path: %s", got)
	}
	if got := withQuery("/chats/x/messages", "$top=5"); got != "/chats/x/messages?$top=5" {
		t.Errorf("unexpected path: %s", got)
	}
}

func TestFilterSince(t *testing.T) {
	cutoff := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	msg := func(id, created, modified string) Message {
		m := Message{ID: id}
		m.CreatedDateTime = created
		m.LastModifiedDateTime = modified
		return m
	}

	in := []Message{
		msg("newer", "2026-10-06T09:00:00Z", "2026-10-06T09:00:00Z"),
		msg("older", "2026-09-01T09:00:00Z", "2026-09-01T09:00:00Z"),
		msg("old-but-edited", "2026-01-01T09:00:00Z", "2026-10-06T10:00:00Z"),
		msg("exactly-at-cutoff", "2026-10-05T00:00:00Z", "2026-10-05T00:00:00Z"),
		msg("falls-back-to-created", "2026-10-07T09:00:00Z", ""),
		msg("unparseable-is-kept", "", ""),
	}
	want := []string{"newer", "old-but-edited", "falls-back-to-created", "unparseable-is-kept"}

	got := filterSince(in, cutoff)
	if len(got) != len(want) {
		t.Fatalf("filterSince returned %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("message %d = %q, want %q", i, got[i].ID, id)
		}
	}
}
