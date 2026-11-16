package crawler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
)

func manifestOf(pages ...models.PageJSONL) *manifestSnapshot {
	return &manifestSnapshot{pages: pages}
}

// TestCheckCrawlMeta covers all four category-4 shapes.
func TestCheckCrawlMeta(t *testing.T) {
	pages := []models.PageJSONL{
		pageRecord("https://docs.example.com/a/", "A", "a", 0),
		pageRecord("https://docs.example.com/b/", "B", "b", 0),
	}

	t.Run("count mismatch", func(t *testing.T) {
		dir := t.TempDir()
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		man := manifestOf(pages...)
		man.metas = []models.CrawlMetaJSONL{metaRecord("site", 99)}
		man.metaLines = []int{2}
		man.totalLines = 3
		findings := v.checkCrawlMeta(man)
		require.Len(t, findings[IssueCrawlMeta], 1)
		assert.Equal(t, "99", findings[IssueCrawlMeta][0].Detail["summary_total_pages"])
		assert.Equal(t, "2", findings[IssueCrawlMeta][0].Detail["page_records"])
	})

	t.Run("multiple summaries", func(t *testing.T) {
		dir := t.TempDir()
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		man := manifestOf(pages...)
		man.metas = []models.CrawlMetaJSONL{metaRecord("site", 2), metaRecord("site", 2)}
		man.metaLines = []int{2, 3}
		man.totalLines = 4
		findings := v.checkCrawlMeta(man)
		require.Len(t, findings[IssueCrawlMeta], 1)
		assert.Contains(t, findings[IssueCrawlMeta][0].Detail["problem"], "more than one")
	})

	t.Run("summary not on last line", func(t *testing.T) {
		dir := t.TempDir()
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		man := manifestOf(pages...)
		man.metas = []models.CrawlMetaJSONL{metaRecord("site", 2)}
		man.metaLines = []int{1}
		man.totalLines = 5
		findings := v.checkCrawlMeta(man)
		require.Len(t, findings[IssueCrawlMeta], 1)
		assert.Contains(t, findings[IssueCrawlMeta][0].Detail["problem"], "last line")
	})

	t.Run("clean", func(t *testing.T) {
		dir := t.TempDir()
		v := newTestVerifier(t, dir, "site", "docs.example.com")
		man := manifestOf(pages...)
		man.metas = []models.CrawlMetaJSONL{metaRecord("site", 2)}
		man.metaLines = []int{2}
		man.totalLines = 3
		assert.Empty(t, v.checkCrawlMeta(man))
	})
}

// TestCheckLLMsFiles_MissingExtraAndOrder covers category 5 for both files.
func TestCheckLLMsFiles_MissingExtraAndOrder(t *testing.T) {
	dir := t.TempDir()
	v := newTestVerifier(t, dir, "site", "docs.example.com")
	man := manifestOf(
		pageRecord("https://docs.example.com/a/", "A", "a", 0),
		pageRecord("https://docs.example.com/b/", "B", "b", 0),
	)

	t.Run("missing files report every manifest entry", func(t *testing.T) {
		findings := v.checkLLMsFiles(man)
		assert.Len(t, findings[IssueLLMsTxt], 2)
		assert.Len(t, findings[IssueLLMsFullTxt], 2)
		for _, e := range findings[IssueLLMsTxt] {
			assert.Equal(t, llmsTxtFilename, e.Path)
		}
	})

	t.Run("extra and missing entries", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, llmsTxtFilename), []byte(
			"# site\n\n> Crawled documentation from docs.example.com. 1 pages.\n\n## Pages\n"+
				"- [A](https://docs.example.com/a/)\n"+
				"- [X](https://docs.example.com/x/)\n",
		), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, llmsFullTxtFilename), []byte(
			"# site\n\n> Full crawled content from docs.example.com.\n\n"+
				"---\n\n# A\n\nURL: https://docs.example.com/a/\n\nbody a\n\n"+
				"---\n\n# X\n\nURL: https://docs.example.com/x/\n\nbody x\n\n",
		), 0o644))

		findings := v.checkLLMsFiles(man)
		txt := findings[IssueLLMsTxt]
		var urls []string
		for _, e := range txt {
			urls = append(urls, e.URL)
		}
		assert.Contains(t, urls, "https://docs.example.com/b/")
		assert.Contains(t, urls, "https://docs.example.com/x/")

		full := findings[IssueLLMsFullTxt]
		var fullURLs []string
		for _, e := range full {
			fullURLs = append(fullURLs, e.URL)
		}
		assert.Contains(t, fullURLs, "https://docs.example.com/b/")
		assert.Contains(t, fullURLs, "https://docs.example.com/x/")
	})

	t.Run("order mismatch", func(t *testing.T) {
		dir2 := t.TempDir()
		v2 := newTestVerifier(t, dir2, "site", "docs.example.com")
		require.NoError(t, os.WriteFile(filepath.Join(dir2, llmsTxtFilename), []byte(
			"# site\n\n## Pages\n"+
				"- [B](https://docs.example.com/b/)\n"+
				"- [A](https://docs.example.com/a/)\n",
		), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir2, llmsFullTxtFilename), []byte(
			"# site\n\n"+
				"---\n\n# B\n\nURL: https://docs.example.com/b/\n\nbody b\n\n"+
				"---\n\n# A\n\nURL: https://docs.example.com/a/\n\nbody a\n\n",
		), 0o644))

		findings := v2.checkLLMsFiles(man)
		var ordered []string
		for _, e := range findings[IssueLLMsTxt] {
			if e.Detail["problem"] == "entry order does not match manifest" {
				ordered = append(ordered, e.URL)
			}
		}
		assert.Contains(t, ordered, "https://docs.example.com/a/")
		assert.NotEmpty(t, findings[IssueLLMsFullTxt])
	})

	t.Run("in sync is clean", func(t *testing.T) {
		dir3 := t.TempDir()
		v3 := newTestVerifier(t, dir3, "site", "docs.example.com")
		require.NoError(t, os.WriteFile(filepath.Join(dir3, llmsTxtFilename), []byte(
			"# site\n\n## Pages\n"+
				"- [A](https://docs.example.com/a/)\n"+
				"- [B](https://docs.example.com/b/)\n",
		), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir3, llmsFullTxtFilename), []byte(
			"# site\n\n"+
				"---\n\n# A\n\nURL: https://docs.example.com/a/\n\nbody a\n\n"+
				"---\n\n# B\n\nURL: https://docs.example.com/b/\n\nbody b\n\n",
		), 0o644))
		findings := v3.checkLLMsFiles(man)
		assert.Empty(t, findings[IssueLLMsTxt])
		assert.Empty(t, findings[IssueLLMsFullTxt])
	})
}

// TestCheckImages_LocalReferencesResolvedFromPageDir covers category 6,
// including that remote and data: references are ignored.
func TestCheckImages_LocalReferencesResolvedFromPageDir(t *testing.T) {
	dir := t.TempDir()
	pageURL := "https://docs.example.com/guide/start/"
	// Page file lives at <dir>/guide/start/index.md; a local image reference
	// "../img/diagram.png" therefore resolves to <dir>/guide/img/diagram.png.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "guide", "img"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "guide", "img", "present.png"), []byte("png"), 0o644))

	p := pageRecord(pageURL, "Start", "body", 1)
	p.Images = []string{
		"../img/present.png",
		"../img/missing.png",
		"https://cdn.example.com/remote.png",
		"data:image/png;base64,AAAA",
	}
	man := manifestOf(p)
	v := newTestVerifier(t, dir, "site", "docs.example.com")

	findings := v.checkImages(man)
	missing := findings[IssueMissingImage]
	require.Len(t, missing, 1)
	assert.Equal(t, "guide/img/missing.png", missing[0].Path)
	assert.Equal(t, pageURL, missing[0].URL)
	assert.Equal(t, "../img/missing.png", missing[0].Detail["reference"])
}
