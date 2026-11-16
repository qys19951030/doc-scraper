package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseVerifyArgs_FlagsAndRejectPositional covers flag parsing and the
// positional-argument guard before the verification logic exists.
func TestParseVerifyArgs_FlagsAndRejectPositional(t *testing.T) {
	a, err := parseVerifyArgs([]string{"-site", "example_docs", "-fix", "-json", "-config", "cfg.yaml"}, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, "example_docs", a.siteKey)
	assert.True(t, a.fix)
	assert.True(t, a.jsonOut)
	assert.Equal(t, "cfg.yaml", a.configFile)

	_, err = parseVerifyArgs([]string{"-site", "example_docs", "extra-arg"}, &bytes.Buffer{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "extra-arg"))
}

// TestDoVerify_MissingSiteArgFails ensures the required -site guard fails
// before touching the filesystem.
func TestDoVerify_MissingSiteArgFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doVerify(verifyArgs{}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "-site is required")
}
