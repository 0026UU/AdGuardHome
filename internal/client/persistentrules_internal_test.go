package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistentSetLoadedRules(t *testing.T) {
	c := &Persistent{Name: "test"}

	dropped, err := c.SetLoadedRules([]string{
		"||keep.test^",
		"||x.test^$badopt=1",
		"@@||free.test^",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"||x.test^$badopt=1"}, dropped)
	assert.Equal(t, []string{"||keep.test^", "@@||free.test^"}, c.Rules)
	assert.NotNil(t, c.RuleEngine())

	// A client whose every rule is broken still starts, it simply has no
	// engine of its own left.
	dropped, err = c.SetLoadedRules([]string{"||y.test^$badopt=1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"||y.test^$badopt=1"}, dropped)
	assert.Empty(t, c.Rules)
	assert.Nil(t, c.RuleEngine())

	// The setter a request goes through still refuses what would be dropped.
	err = c.SetRules([]string{"||z.test^$badopt=1"})
	require.Error(t, err)
	assert.Empty(t, c.Rules)
}
