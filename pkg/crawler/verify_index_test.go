package crawler

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/chunk"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

func newTestIndex(t *testing.T) (*index.Index, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	idx, err := index.Open(path, 0, silentLogger())
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx, path
}

func chunkBody(body string) []chunk.Chunk {
	return chunk.Split(body)
}

// TestCheckIndex_MissingStaleAndStaleHash covers all three category-7 shapes.
func TestCheckIndex_MissingStaleAndStaleHash(t *testing.T) {
	idx, path := newTestIndex(t)
	ctx := context.Background()

	urlA := "https://docs.example.com/a/"
	urlB := "https://docs.example.com/b/"
	urlX := "https://docs.example.com/x/"
	hashA := utils.CalculateStringSHA256("body a")
	hashB := utils.CalculateStringSHA256("body b")

	require.NoError(t, idx.ReplaceChunks(ctx, "site", urlA, "A", hashA, chunkBody("body a")))
	// b is indexed under an old hash; x is indexed but no longer in the manifest.
	require.NoError(t, idx.ReplaceChunks(ctx, "site", urlB, "B", utils.CalculateStringSHA256("old body"), chunkBody("old body")))
	require.NoError(t, idx.ReplaceChunks(ctx, "site", urlX, "X", utils.CalculateStringSHA256("body x"), chunkBody("body x")))

	man := manifestOf(
		pageRecord(urlA, "A", "body a", 0),
		pageRecord(urlB, "B", "body b", 0),
	)
	v := newTestVerifier(t, t.TempDir(), "site", "docs.example.com")
	v.indexPath = path

	findings := v.checkIndex(ctx, man, idx)

	missing := findings[IssueIndexMissingPage]
	var urls []string
	for _, e := range missing {
		urls = append(urls, e.URL)
	}
	assert.Contains(t, urls, urlB) // stale hash counts as missing/current
	for _, e := range missing {
		if e.URL == urlB {
			assert.Equal(t, hashB, e.Detail["manifest_hash"])
		}
	}

	stale := findings[IssueIndexStalePage]
	require.Len(t, stale, 1)
	assert.Equal(t, urlX, stale[0].URL)
}

// TestCheckIndex_NilIndexReportsEveryPageMissing covers the "no index
// database file" case without erroring.
func TestCheckIndex_NilIndexReportsEveryPageMissing(t *testing.T) {
	man := manifestOf(pageRecord("https://docs.example.com/a/", "A", "body", 0))
	v := newTestVerifier(t, t.TempDir(), "site", "docs.example.com")
	findings := v.checkIndex(context.Background(), man, nil)
	require.Len(t, findings[IssueIndexMissingPage], 1)
	assert.Empty(t, findings[IssueIndexStalePage])
}

// TestCheckIndex_InSyncIsClean proves a matching index yields no findings.
func TestCheckIndex_InSyncIsClean(t *testing.T) {
	idx, _ := newTestIndex(t)
	ctx := context.Background()
	url := "https://docs.example.com/a/"
	body := "body a"
	require.NoError(t, idx.ReplaceChunks(ctx, "site", url, "A", utils.CalculateStringSHA256(body), chunkBody(body)))

	man := manifestOf(pageRecord(url, "A", body, 0))
	v := newTestVerifier(t, t.TempDir(), "site", "docs.example.com")
	assert.Empty(t, v.checkIndex(ctx, man, idx))
}

// TestRenderReports_HumanAndJSONCarrySameCounts ensures both report forms
// expose identical categories, counts and entries, and that a clean report
// says so.
func TestRenderReports_HumanAndJSONCarrySameCounts(t *testing.T) {
	r := &VerifyReport{
		SiteKey:   "site",
		OutputDir: "/tmp/out",
		IndexPath: "/tmp/state/index.db",
		Findings: []IssueGroup{
			{
				Category: IssueMissingMarkdown,
				Count:    1,
				Entries: []IssueEntry{{
					Category: IssueMissingMarkdown,
					Path:     "b/index.md",
					URL:      "https://docs.example.com/b/",
				}},
			},
		},
		FindingsCount: 1,
	}

	var human bytes.Buffer
	RenderVerifyHuman(&human, r)
	humanText := human.String()
	assert.Contains(t, humanText, "site")
	assert.Contains(t, humanText, IssueMissingMarkdown)
	assert.Contains(t, humanText, "b/index.md")
	assert.Contains(t, humanText, "https://docs.example.com/b/")
	assert.Contains(t, humanText, "1")

	var structured bytes.Buffer
	require.NoError(t, RenderVerifyJSON(&structured, r))
	assert.Contains(t, structured.String(), `"category": "missing_markdown"`)
	assert.Contains(t, structured.String(), `"findings_count": 1`)
	assert.Contains(t, structured.String(), "b/index.md")

	// A clean report renders the all-clear line in both forms.
	clean := &VerifyReport{SiteKey: "site", OutputDir: "/tmp/out", Findings: []IssueGroup{}}
	var cleanHuman bytes.Buffer
	RenderVerifyHuman(&cleanHuman, clean)
	assert.Contains(t, cleanHuman.String(), "No inconsistencies found.")
	var cleanJSON bytes.Buffer
	require.NoError(t, RenderVerifyJSON(&cleanJSON, clean))
	assert.Contains(t, cleanJSON.String(), `"findings_count": 0`)
}
