package crawler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

// writePageFile writes a Markdown page exactly like the crawler would:
// YAML front matter (with the body's hash) followed by the body.
func writePageFile(t *testing.T, path, title, pageURL, body string, depth int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	fm := verifyFrontMatter{
		Title:       title,
		URL:         pageURL,
		CrawledAt:   "2026-01-01T00:00:00Z",
		ContentHash: utils.CalculateStringSHA256(body),
		Depth:       depth,
	}
	content := "---\n" + mustYAML(t, fm) + "---\n\n" + body
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func mustYAML(t *testing.T, v any) string {
	t.Helper()
	b, err := yamlMarshal(v)
	require.NoError(t, err)
	return string(b)
}

func yamlMarshal(v any) ([]byte, error) {
	return yaml.Marshal(v)
}

// pageRecord builds a manifest record consistent with a page body.
func pageRecord(url, title, body string, depth int) models.PageJSONL {
	return models.PageJSONL{
		RecordType:  models.RecordTypePage,
		URL:         url,
		Title:       title,
		Content:     body,
		ContentHash: utils.CalculateStringSHA256(body),
		CrawledAt:   "2026-01-01T00:00:00Z",
		Depth:       depth,
	}
}

func metaRecord(siteKey string, total int) models.CrawlMetaJSONL {
	return models.CrawlMetaJSONL{
		RecordType:     models.RecordTypeCrawlMeta,
		SiteKey:        siteKey,
		AllowedDomain:  "docs.example.com",
		CrawlStartedAt: "2026-01-01T00:00:00Z",
		CrawlEndedAt:   "2026-01-01T00:01:00Z",
		TotalPages:     total,
	}
}

// writeManifest writes pages followed by one crawl_meta as the last line.
func writeManifest(t *testing.T, dir string, pages []models.PageJSONL, meta *models.CrawlMetaJSONL) {
	t.Helper()
	records := make([]any, 0, len(pages)+1)
	for i := range pages {
		records = append(records, pages[i])
	}
	if meta != nil {
		records = append(records, *meta)
	}
	writeTestJSONL(t, filepath.Join(dir, "pages.jsonl"), records)
}

// TestCheckPreconditions covers every required up-front failure.
func TestCheckPreconditions(t *testing.T) {
	t.Run("missing output directory", func(t *testing.T) {
		v := newTestVerifier(t, filepath.Join(t.TempDir(), "gone"), "site", "docs.example.com")
		err := v.CheckPreconditions()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrVerifyPrecondition)
		assert.Contains(t, err.Error(), "output directory does not exist")
	})

	t.Run("missing manifest", func(t *testing.T) {
		dir := t.TempDir()
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		err := v.CheckPreconditions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manifest does not exist")
	})

	t.Run("empty manifest", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pages.jsonl"), nil, 0o644))
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		err := v.CheckPreconditions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manifest is empty")
	})

	t.Run("no page records", func(t *testing.T) {
		dir := t.TempDir()
		// A non-empty file whose only line is not a page record.
		writeTestJSONL(t, filepath.Join(dir, "pages.jsonl"), []any{
			models.CrawlMetaJSONL{RecordType: models.RecordTypeCrawlMeta, SiteKey: "site", TotalPages: 0},
		})
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		err := v.CheckPreconditions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no page records")
	})

	t.Run("no crawl_meta record", func(t *testing.T) {
		dir := t.TempDir()
		writeManifest(t, dir, []models.PageJSONL{pageRecord("https://docs.example.com/a", "A", "body", 0)}, nil)
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		err := v.CheckPreconditions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "crawl_meta")
	})

	t.Run("valid corpus passes", func(t *testing.T) {
		dir := t.TempDir()
		body := "# Hello\n"
		writePageFile(t, filepath.Join(dir, "index.md"), "Home", "https://docs.example.com/", body, 0)
		writeManifest(t, dir, []models.PageJSONL{pageRecord("https://docs.example.com/", "Home", body, 0)}, ptr(metaRecord("site", 1)))
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		assert.NoError(t, v.CheckPreconditions())
	})
}

// TestCheckMarkdownPresence_DetectsMissingAndOrphan covers categories 1 and 2
// with concrete relative paths and URLs.
func TestCheckMarkdownPresence_DetectsMissingAndOrphan(t *testing.T) {
	dir := t.TempDir()
	bodyA, bodyB := "# A\n", "# B\n"
	urlA := "https://docs.example.com/a/"
	urlB := "https://docs.example.com/b/"
	writePageFile(t, filepath.Join(dir, "a", "index.md"), "A", urlA, bodyA, 0)
	// The manifest also knows /b, whose Markdown file is missing; an extra
	// orphan file is added too.
	writePageFile(t, filepath.Join(dir, "orphan", "index.md"), "Orphan", "https://docs.example.com/orphan/", "# o\n", 1)

	// Manifest knows /a (file present), /b (file missing), not /orphan.
	man := &manifestSnapshot{
		pages: []models.PageJSONL{
			pageRecord(urlA, "A", bodyA, 0),
			pageRecord(urlB, "B", bodyB, 0),
		},
	}
	disk, err := newTestVerifier(t, dir, "site", "docs.example.com").scanDiskPages()
	require.NoError(t, err)
	require.Len(t, disk, 2)

	findings := newTestVerifier(t, dir, "site", "docs.example.com").checkMarkdownPresence(man, disk)

	missing := findings[IssueMissingMarkdown]
	require.Len(t, missing, 1)
	assert.Equal(t, "b/index.md", missing[0].Path)
	assert.Equal(t, urlB, missing[0].URL)

	orphans := findings[IssueOrphanMarkdown]
	require.Len(t, orphans, 1)
	assert.Equal(t, "orphan/index.md", orphans[0].Path)
	assert.Equal(t, "https://docs.example.com/orphan/", orphans[0].URL)
}

// TestCheckFingerprints_DetectsEditedBody covers category 3, including the
// original manifest hash being reported for later repair bookkeeping.
func TestCheckFingerprints_DetectsEditedBody(t *testing.T) {
	dir := t.TempDir()
	url := "https://docs.example.com/a"
	original := "# Original\n"
	edited := "# Someone edited this on disk\n"

	writePageFile(t, filepath.Join(dir, "a.md"), "A", url, edited, 0)
	man := &manifestSnapshot{pages: []models.PageJSONL{pageRecord(url, "A", original, 0)}}

	v := newTestVerifier(t, dir, "site", "docs.example.com")
	disk, err := v.scanDiskPages()
	require.NoError(t, err)

	findings := v.checkFingerprints(man, disk)
	mismatch := findings[IssueHashMismatch]
	require.Len(t, mismatch, 1)
	assert.Equal(t, "a.md", mismatch[0].Path)
	assert.Equal(t, url, mismatch[0].URL)
	assert.Equal(t, utils.CalculateStringSHA256(original), mismatch[0].Detail["manifest_hash"])
	assert.Equal(t, utils.CalculateStringSHA256(edited), mismatch[0].Detail["disk_hash"])
}

// TestCheckFingerprints_MatchingBodyIsClean ensures an untouched corpus
// produces no fingerprint findings.
func TestCheckFingerprints_MatchingBodyIsClean(t *testing.T) {
	dir := t.TempDir()
	url := "https://docs.example.com/a"
	body := "# Same on both sides\n"
	writePageFile(t, filepath.Join(dir, "a.md"), "A", url, body, 0)
	man := &manifestSnapshot{pages: []models.PageJSONL{pageRecord(url, "A", body, 0)}}

	v := newTestVerifier(t, dir, "site", "docs.example.com")
	disk, err := v.scanDiskPages()
	require.NoError(t, err)
	assert.Empty(t, v.checkFingerprints(man, disk))
}

// TestScanDiskPages_IgnoresNonPageMarkdown ensures random .md files without
// page front matter are not treated as pages.
func TestScanDiskPages_IgnoresNonPageMarkdown(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# just notes\n"), 0o644))
	writePageFile(t, filepath.Join(dir, "a.md"), "A", "https://docs.example.com/a", "body\n", 0)

	pages, err := newTestVerifier(t, dir, "site", "docs.example.com").scanDiskPages()
	require.NoError(t, err)
	require.Len(t, pages, 1)
	assert.Equal(t, "a.md", pages[0].relPath)
	assert.Equal(t, utils.CalculateStringSHA256("body\n"), pages[0].bodyHash)
}

// TestParseMarkdownPage_RoundTripsCrawlerFormat proves the parser splits the
// envelope process.ContentProcessor writes.
func TestParseMarkdownPage_RoundTripsCrawlerFormat(t *testing.T) {
	body := "# Title\n\nsome content\n"
	fm := verifyFrontMatter{
		Title:       "Title",
		URL:         "https://docs.example.com/a",
		CrawledAt:   "2026-01-01T00:00:00Z",
		ContentHash: utils.CalculateStringSHA256(body),
	}
	raw := "---\n" + mustYAML(t, fm) + "---\n\n" + body
	gotFM, gotBody, ok := parseMarkdownPage([]byte(raw))
	require.True(t, ok)
	assert.Equal(t, "https://docs.example.com/a", gotFM.URL)
	assert.Equal(t, body, gotBody)
	assert.Equal(t, utils.CalculateStringSHA256(body), gotFM.ContentHash)
}

func ptr[T any](x T) *T { return &x }
