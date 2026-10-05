package graph

import (
	"testing"
	"time"
)

func TestMessageQueryString(t *testing.T) {
	since := time.Date(2026, 10, 5, 0, 0, 0, 0, time.FixedZone("EEST", 3*3600))

	cases := []struct {
		name string
		q    MessageQuery
		want string
	}{
		{"zero value adds nothing", MessageQuery{}, ""},
		{
			"since only",
			MessageQuery{Since: since},
			"$orderby=lastModifiedDateTime%20desc&$top=50&$filter=lastModifiedDateTime%20gt%202026-10-04T21%3A00%3A00Z",
		},
		{
			"limit only",
			MessageQuery{Limit: 20},
			"$orderby=lastModifiedDateTime%20desc&$top=20",
		},
		{
			"limit above page size is capped to page size",
			MessageQuery{Limit: 500},
			"$orderby=lastModifiedDateTime%20desc&$top=50",
		},
	}
	for _, c := range cases {
		if got := c.q.query(); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

func TestWithQuery(t *testing.T) {
	if got := withQuery("/chats/x/messages", MessageQuery{}); got != "/chats/x/messages" {
		t.Errorf("zero query changed path: %s", got)
	}
	if got := withQuery("/chats/x/messages", MessageQuery{Limit: 5}); got != "/chats/x/messages?$orderby=lastModifiedDateTime%20desc&$top=5" {
		t.Errorf("unexpected path: %s", got)
	}
}
