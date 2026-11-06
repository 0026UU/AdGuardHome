package querylog

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/AdguardTeam/AdGuardHome/internal/filtering"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchCriterion_ClientMatch(t *testing.T) {
	entry := &logEntry{
		ClientID: "client-1",
		IP:       net.IP{1, 2, 3, 4},
		client:   &Client{Name: "Known Client"},
	}

	testCases := []struct {
		name   string
		values []string
		want   assert.BoolAssertionFunc
	}{{
		name:   "client_id",
		values: []string{"CLIENT-1"},
		want:   assert.True,
	}, {
		name:   "ip",
		values: []string{"1.2.3.4"},
		want:   assert.True,
	}, {
		name:   "name",
		values: []string{"known client"},
		want:   assert.True,
	}, {
		name:   "second_of_many",
		values: []string{"10.9.9.9", "testclient", "1.2.3.4"},
		want:   assert.True,
	}, {
		name:   "no_match",
		values: []string{"10.9.9.9", "unknown"},
		want:   assert.False,
	}, {
		name:   "empty_value_does_not_match_empty_fields",
		values: []string{""},
		want:   assert.False,
	}, {
		name:   "no_values",
		values: nil,
		want:   assert.False,
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := &searchCriterion{criterionType: ctClient, values: tc.values}

			tc.want(t, c.match(entry))
		})
	}

	t.Run("no_resolved_client", func(t *testing.T) {
		e := &logEntry{IP: net.IP{1, 2, 3, 4}}

		byIP := &searchCriterion{criterionType: ctClient, values: []string{"1.2.3.4"}}
		assert.True(t, byIP.match(e))

		byName := &searchCriterion{criterionType: ctClient, values: []string{"Known Client"}}
		assert.False(t, byName.match(e))

		byEmpty := &searchCriterion{criterionType: ctClient, values: []string{""}}
		assert.False(t, byEmpty.match(e))
	})
}

func TestSearchCriterion_QuickMatchClient(t *testing.T) {
	line, err := json.Marshal(&logEntry{
		ClientID: "client-1",
		IP:       net.IP{1, 2, 3, 4},
	})
	require.NoError(t, err)

	findClient := func(_ context.Context, _ *slog.Logger, clientID, _ string) (c *Client) {
		if clientID == "client-1" {
			return &Client{Name: "Known Client"}
		}

		return nil
	}

	ctx := testutil.ContextWithTimeout(t, testTimeout)

	byName := &searchCriterion{criterionType: ctClient, values: []string{"KNOWN CLIENT"}}
	assert.True(t, byName.quickMatch(ctx, testLogger, string(line), findClient))

	byIP := &searchCriterion{criterionType: ctClient, values: []string{"10.9.9.9", "1.2.3.4"}}
	assert.True(t, byIP.quickMatch(ctx, testLogger, string(line), findClient))

	byMiss := &searchCriterion{criterionType: ctClient, values: []string{"unknown"}}
	assert.False(t, byMiss.quickMatch(ctx, testLogger, string(line), findClient))
}

func TestSearchCriterion_MultipleReasons(t *testing.T) {
	c := &searchCriterion{
		criterionType: ctReason,
		values:        []string{"FilteredBlackList", "NotFilteredError"},
	}

	assert.True(t, c.match(&logEntry{
		Result: filtering.Result{Reason: filtering.FilteredBlockList},
	}))
	assert.True(t, c.match(&logEntry{
		Result: filtering.Result{Reason: filtering.NotFilteredError},
	}))
	assert.False(t, c.match(&logEntry{
		Result: filtering.Result{Reason: filtering.NotFilteredNotFound},
	}))
}

func TestSearchParams_Since(t *testing.T) {
	now := time.Now()

	sp := newSearchParams()
	assert.True(t, sp.match(&logEntry{Time: now}))

	sp.since = now.Add(-time.Hour)
	assert.True(t, sp.match(&logEntry{Time: now}))

	sp.since = now.Add(time.Hour)
	assert.False(t, sp.match(&logEntry{Time: now}))

	sp.since = time.Time{}
	sp.olderThan = now.Add(-time.Minute)
	assert.False(t, sp.match(&logEntry{Time: now}))
}

func TestQueryLog_ParseSearchParams_ForkCriteria(t *testing.T) {
	l := &queryLog{logger: testLogger}
	ctx := testutil.ContextWithTimeout(t, testTimeout)

	t.Run("client_and_reason_and_since", func(t *testing.T) {
		const since = "2024-01-02T15:04:05Z"
		r := &http.Request{URL: &url.URL{RawQuery: "client=1.2.3.4" +
			"&client=testclient" +
			"&reason=FilteredBlackList" +
			"&reason=NotFilteredError" +
			"&since=" + since}}

		p, err := l.parseSearchParams(ctx, r)
		require.NoError(t, err)

		wantSince, err := time.Parse(time.RFC3339Nano, since)
		require.NoError(t, err)
		assert.Equal(t, wantSince, p.since)

		require.Len(t, p.searchCriteria, 2)

		var client, reason *searchCriterion
		for i := range p.searchCriteria {
			c := &p.searchCriteria[i]
			switch c.criterionType {
			case ctClient:
				client = c
			case ctReason:
				reason = c
			}
		}

		require.NotNil(t, client)
		assert.Equal(t, []string{"1.2.3.4", "testclient"}, client.values)

		require.NotNil(t, reason)
		assert.Equal(t, []string{"FilteredBlackList", "NotFilteredError"}, reason.values)
	})

	t.Run("bad_since", func(t *testing.T) {
		r := &http.Request{URL: &url.URL{RawQuery: "since=not-a-time"}}

		_, err := l.parseSearchParams(ctx, r)
		assert.Error(t, err)
	})

	t.Run("bad_reason", func(t *testing.T) {
		r := &http.Request{URL: &url.URL{RawQuery: "reason=NoSuchReason"}}

		_, err := l.parseSearchParams(ctx, r)
		assert.Error(t, err)
	})

	t.Run("reason_with_response_status", func(t *testing.T) {
		r := &http.Request{URL: &url.URL{RawQuery: "reason=FilteredBlockList&response_status=all"}}

		_, err := l.parseSearchParams(ctx, r)
		assert.Error(t, err)
	})
}
