package crawler

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/config"
)

func newTestVerifier(t *testing.T, dir, siteKey, domain string) *Verifier {
	t.Helper()
	appCfg := &config.AppConfig{}
	siteCfg := &config.SiteConfig{AllowedDomain: domain, AllowedPathPrefix: "/"}
	return NewVerifier(appCfg, siteCfg, siteKey, dir, "pages.jsonl", silentLogger())
}

// TestVerifier_ExpectedMarkdownPathReusesCrawlerMapping pins the verifier's
// URL->path mapping to the same rule the crawler saves pages with, so a
// missing-file finding can never be an artifact of divergent path logic.
func TestVerifier_ExpectedMarkdownPathReusesCrawlerMapping(t *testing.T) {
	dir := t.TempDir()
	v := newTestVerifier(t, dir, "example_docs", "docs.example.com")

	p, ok := v.expectedMarkdownPath("https://docs.example.com/guide/start.html")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "guide", "start.md"), p)

	// Out-of-scope URLs do not map.
	_, ok = v.expectedMarkdownPath("https://elsewhere.example.com/guide")
	assert.False(t, ok)
}

// TestPreconditionError_Classifiable ensures callers can classify the
// precondition failures via errors.As/Is once the checks are implemented.
func TestPreconditionError_Classifiable(t *testing.T) {
	err := &PreconditionError{Reason: "output directory does not exist", Path: "/tmp/nope"}
	assert.Contains(t, err.Error(), "output directory does not exist")
	assert.ErrorIs(t, err, ErrVerifyPrecondition)
}
