package filtering

import (
	"testing"

	"github.com/AdguardTeam/urlfilter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRuleEngineMatchesRules(t *testing.T) {
	e, err := NewClientRuleEngine([]string{
		"! a comment, which is not a rule",
		"",
		"||block.test^",
		"@@||allow.test^",
	})
	require.NoError(t, err)
	require.NotNil(t, e)

	dnsres, ok := e.MatchRequest(&urlfilter.DNSRequest{
		Hostname: "sub.block.test",
		DNSType:  1,
	})
	require.True(t, ok)
	require.NotNil(t, dnsres.NetworkRule)
	assert.Equal(t, "||block.test^", dnsres.NetworkRule.Text())
	assert.False(t, dnsres.NetworkRule.Whitelist)

	dnsres, ok = e.MatchRequest(&urlfilter.DNSRequest{
		Hostname: "allow.test",
		DNSType:  1,
	})
	require.True(t, ok)
	require.NotNil(t, dnsres.NetworkRule)
	assert.Equal(t, "@@||allow.test^", dnsres.NetworkRule.Text())
	assert.True(t, dnsres.NetworkRule.Whitelist)

	dnsres, ok = e.MatchRequest(&urlfilter.DNSRequest{
		Hostname: "nothing.test",
		DNSType:  1,
	})
	assert.False(t, ok)
	assert.Nil(t, dnsres.NetworkRule)
}

func TestClientRuleEngineNilWithoutRules(t *testing.T) {
	e, err := NewClientRuleEngine(nil)
	require.NoError(t, err)
	assert.Nil(t, e)
}

func TestClientRuleEngineRefusesUncompilable(t *testing.T) {
	// urlfilter parses a list lazily and drops the lines it cannot make a rule
	// of, so these are the ones the engine would swallow in silence.
	for _, text := range []string{
		"||x.test^$unknownoption123",
		"||x.test^$important,~nope",
		"||x.test^$badopt=1",
		"||x.test^$dnstype=NOPE",
	} {
		t.Run(text, func(t *testing.T) {
			e, err := NewClientRuleEngine([]string{text})
			require.Error(t, err)
			assert.Nil(t, e)
		})
	}
}

func TestClientRuleEngineRefusesWholeWrite(t *testing.T) {
	// One bad rule must refuse the list instead of storing the good ones and
	// pretending the client is protected.
	e, err := NewClientRuleEngine([]string{
		"||guard.test^",
		"||x.test^$badopt=1",
	})
	require.Error(t, err)
	assert.Nil(t, e)
}

func TestClientRuleEngineAcceptsWhatItStores(t *testing.T) {
	// Cosmetic rules are dropped by the engine, but they do parse, and a plain
	// string of characters is a valid rule of its own.  Refusing them would
	// make the editor unusable.
	for _, text := range []string{
		"example.com##div.ad",
		"[[[BAD",
		"@@@@bad",
	} {
		t.Run(text, func(t *testing.T) {
			e, err := NewClientRuleEngine([]string{text})
			require.NoError(t, err)
			assert.NotNil(t, e)
		})
	}
}
