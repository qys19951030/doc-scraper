package crawler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/chunk"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

// brokenCorpus builds a corpus with every repairable defect at once:
//   - /b/: manifest record with no Markdown file (unrepairable -> report only)
//   - /orphan/: Markdown file with no manifest record (repairable -> add)
//   - /a/: disk body edited, manifest hash stale (repairable -> update hash)
//   - crawl_meta count wrong and llms files missing
//   - search index stale (holds /b, misses /a current hash)
func brokenCorpusFixture(t *testing.T) (string, *Verifier, *index.Index, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	v := newTestVerifier(t, dir, "site", "docs.example.com")

	urlA := "https://docs.example.com/a/"
	urlB := "https://docs.example.com/b/"
	urlOrphan := "https://docs.example.com/orphan/"

	originalA := "# A original\n"
	editedA := "# A edited on disk\n"
	orphanBody := "# Orphan page\n"

	writePageFile(t, filepath.Join(dir, "a", "index.md"), "A", urlA, editedA, 0)
	writePageFile(t, filepath.Join(dir, "orphan", "index.md"), "Orphan", urlOrphan, orphanBody, 1)
	// Note: no b/index.md on disk.

	pages := []models.PageJSONL{
		pageRecord(urlA, "A", originalA, 0),
		pageRecord(urlB, "B", "# B\n", 0),
	}
	writeManifest(t, dir, pages, ptr(metaRecord("site", 99)))

	idx, idxPath := newTestIndex(t)
	ctx := context.Background()
	require.NoError(t, idx.ReplaceChunks(ctx, "site", urlB, "B", utils.CalculateStringSHA256("# B\n"), chunk.Split("# B\n")))
	v.indexPath = idxPath

	return dir, v, idx, idxPath, map[string]string{
		"a":      urlA,
		"b":      urlB,
		"orphan": urlOrphan,
	}
}

// TestVerifyAndFix_RepairsEverythingRepairable drives one fixing run over a
// corpus with every defect class and asserts the post-repair state.
func TestVerifyAndFix_RepairsEverythingRepairable(t *testing.T) {
	dir, v, idx, idxPath, urls := brokenCorpusFixture(t)
	ctx := context.Background()

	report, err := v.VerifyAndFix(ctx, idxPath)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.True(t, report.Fixed)

	// Findings must name concrete entries before the repair.
	cats := map[string]bool{}
	for _, g := range report.Findings {
		cats[g.Category] = true
		assert.Greater(t, g.Count, 0)
	}
	assert.True(t, cats[IssueMissingMarkdown])
	assert.True(t, cats[IssueOrphanMarkdown])
	assert.True(t, cats[IssueHashMismatch])
	assert.True(t, cats[IssueCrawlMeta])
	assert.True(t, cats[IssueLLMsTxt])
	assert.True(t, cats[IssueLLMsFullTxt])
	assert.True(t, cats[IssueIndexMissingPage] || cats[IssueIndexStalePage])

	// A hash update fix must retain the original manifest hash for auditing.
	var hashFix *FixEntry
	for i := range report.Fixes {
		if report.Fixes[i].Category == IssueHashMismatch {
			hashFix = &report.Fixes[i]
		}
	}
	require.NotNil(t, hashFix)
	assert.Equal(t, urls["a"], hashFix.URL)
	assert.Equal(t, utils.CalculateStringSHA256("# A original\n"), hashFix.Detail["old_hash"])
	assert.Equal(t, utils.CalculateStringSHA256("# A edited on disk\n"), hashFix.Detail["new_hash"])

	// Residual: only the locally unrepairable missing page remains.
	residualCats := map[string]int{}
	for _, g := range report.Residual {
		residualCats[g.Category] = g.Count
	}
	assert.Equal(t, 1, residualCats[IssueMissingMarkdown])
	for _, c := range []string{
		IssueOrphanMarkdown, IssueHashMismatch, IssueCrawlMeta,
		IssueLLMsTxt, IssueLLMsFullTxt, IssueIndexMissingPage, IssueIndexStalePage,
	} {
		assert.NotContains(t, residualCats, c, "category %s should be fully repaired", c)
	}

	// The rewritten manifest: /a updated, /b removed, /orphan added, meta last.
	man, err := v.loadManifest()
	require.NoError(t, err)
	require.Len(t, man.pages, 2)
	assert.Equal(t, urls["a"], man.pages[0].URL) // URL-sorted: a < orphan
	assert.Equal(t, urls["orphan"], man.pages[1].URL)
	assert.Equal(t, utils.CalculateStringSHA256("# A edited on disk\n"), man.pages[0].ContentHash)
	assert.Equal(t, "# A edited on disk\n", man.pages[0].Content)
	require.Len(t, man.metas, 1)
	assert.Equal(t, 2, man.metas[0].TotalPages)
	assert.Equal(t, man.totalLines-1, man.metaLines[0])

	// llms files regenerated in manifest order.
	indexEntries, err := parseLLMsIndex(filepath.Join(dir, llmsTxtFilename))
	require.NoError(t, err)
	require.Len(t, indexEntries, 2)
	assert.Equal(t, urls["a"], indexEntries[0].url)
	assert.Equal(t, urls["orphan"], indexEntries[1].url)
	fullEntries, err := parseLLMsFull(filepath.Join(dir, llmsFullTxtFilename))
	require.NoError(t, err)
	assert.Len(t, fullEntries, 2)

	// Search index rebuilt to match exactly.
	hashes, err := idx.ChunkedHashes(ctx, "site")
	require.NoError(t, err)
	assert.Len(t, hashes, 2)
	assert.Contains(t, hashes, urls["a"])
	assert.Contains(t, hashes, urls["orphan"])
	assert.NotContains(t, hashes, urls["b"])
	assert.Equal(t, utils.CalculateStringSHA256("# A edited on disk\n"), hashes[urls["a"]])
}

// TestVerifyAndFix_IsIdempotent proves a second fixing run changes nothing.
func TestVerifyAndFix_IsIdempotent(t *testing.T) {
	_, v, _, idxPath, _ := brokenCorpusFixture(t)

	first, err := v.VerifyAndFix(context.Background(), idxPath)
	require.NoError(t, err)
	manBefore, err := os.ReadFile(filepath.Join(v.dir, v.jsonl))
	require.NoError(t, err)
	llmsBefore, err := os.ReadFile(filepath.Join(v.dir, llmsTxtFilename))
	require.NoError(t, err)

	second, err := v.VerifyAndFix(context.Background(), idxPath)
	require.NoError(t, err)

	manAfter, err := os.ReadFile(filepath.Join(v.dir, v.jsonl))
	require.NoError(t, err)
	llmsAfter, err := os.ReadFile(filepath.Join(v.dir, llmsTxtFilename))
	require.NoError(t, err)
	assert.Equal(t, manBefore, manAfter, "manifest must be byte-stable across reruns")
	assert.Equal(t, llmsBefore, llmsAfter, "llms.txt must be byte-stable across reruns")

	// The unrepairable missing page is named in the first run's residual; the
	// rerun has nothing left to change and applies no further fixes.
	assert.GreaterOrEqual(t, first.ResidualCount, 1)
	assert.Equal(t, 0, second.FindingsCount, "the re-scan of a repaired corpus is clean")
	assert.Empty(t, second.Fixes, "a rerun over a repaired corpus applies no fixes")
	assert.Equal(t, 0, second.ResidualCount)
}

// TestVerifyAndFix_CleanCorpusAppliesNoFixes ensures an already-consistent
// corpus is reported clean and left untouched.
func TestVerifyAndFix_CleanCorpusAppliesNoFixes(t *testing.T) {
	dir := t.TempDir()
	v := newTestVerifier(t, dir, "site", "docs.example.com")
	url := "https://docs.example.com/a/"
	body := "# A\n"
	writePageFile(t, filepath.Join(dir, "a", "index.md"), "A", url, body, 0)
	writeManifest(t, dir, []models.PageJSONL{pageRecord(url, "A", body, 0)}, ptr(metaRecord("site", 1)))
	require.NoError(t, v.writeLLMsFiles())

	idx, idxPath := newTestIndex(t)
	require.NoError(t, v.rebuildSearchIndex(context.Background(), idx,
		[]models.PageJSONL{pageRecord(url, "A", body, 0)}))

	report, err := v.VerifyAndFix(context.Background(), idxPath)
	require.NoError(t, err)
	assert.Equal(t, 0, report.FindingsCount)
	assert.Empty(t, report.Fixes)
	assert.Equal(t, 0, report.ResidualCount)
}

// TestVerifyAndFix_MissingImageStaysResidual proves a missing local image is
// reported but never guessed at during repair.
func TestVerifyAndFix_MissingImageStaysResidual(t *testing.T) {
	dir := t.TempDir()
	v := newTestVerifier(t, dir, "site", "docs.example.com")
	url := "https://docs.example.com/a/"
	body := "# A\n"
	writePageFile(t, filepath.Join(dir, "a", "index.md"), "A", url, body, 0)
	p := pageRecord(url, "A", body, 0)
	p.Images = []string{"img/gone.png"}
	writeManifest(t, dir, []models.PageJSONL{p}, ptr(metaRecord("site", 1)))

	report, err := v.VerifyAndFix(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	require.NoError(t, err)
	residual := map[string]int{}
	for _, g := range report.Residual {
		residual[g.Category] = g.Count
	}
	assert.Equal(t, 1, residual[IssueMissingImage])
	// No image file was fabricated.
	_, statErr := os.Stat(filepath.Join(dir, "a", "img", "gone.png"))
	assert.True(t, os.IsNotExist(statErr))
}
