package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/config"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/crawler"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

// cliVerifyFixture lays down a config plus a site output directory and
// returns the config path and key. Corpus files are added by the test.
type cliVerifyFixture struct {
	dir     string
	cfgPath string
	siteKey string
	outDir  string
}

func newCLIVerifyFixture(t *testing.T) *cliVerifyFixture {
	t.Helper()
	dir := t.TempDir()
	f := &cliVerifyFixture{
		dir:     dir,
		cfgPath: filepath.Join(dir, "config.yaml"),
		siteKey: "example_docs",
		outDir:  filepath.Join(dir, "out", "example_docs"),
	}
	require.NoError(t, os.MkdirAll(f.outDir, 0o755))
	cfg := map[string]any{
		"output_base_dir": filepath.Join(dir, "out"),
		"state_dir":       filepath.Join(dir, "state"),
		"sites": map[string]any{
			f.siteKey: map[string]any{
				"allowed_domain":      "docs.example.com",
				"allowed_path_prefix": "/",
				"start_urls":          []string{"https://docs.example.com/"},
			},
		},
	}
	raw, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.cfgPath, raw, 0o644))
	return f
}

func (f *cliVerifyFixture) writePage(t *testing.T, rel, url, title, body string, depth int) {
	t.Helper()
	path := filepath.Join(f.outDir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	content := "---\n" +
		"title: " + title + "\n" +
		"url: " + url + "\n" +
		"crawled_at: 2026-01-01T00:00:00Z\n" +
		"content_hash: " + utils.CalculateStringSHA256(body) + "\n" +
		"depth: 0\n" +
		"---\n\n" + body
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func (f *cliVerifyFixture) writeManifest(t *testing.T, pages []models.PageJSONL, total int) {
	t.Helper()
	records := make([]any, 0, len(pages)+1)
	for i := range pages {
		records = append(records, pages[i])
	}
	records = append(records, models.CrawlMetaJSONL{
		RecordType:    models.RecordTypeCrawlMeta,
		SiteKey:       f.siteKey,
		AllowedDomain: "docs.example.com",
		TotalPages:    total,
	})
	path := filepath.Join(f.outDir, "pages.jsonl")
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()
	enc := json.NewEncoder(file)
	for _, r := range records {
		require.NoError(t, enc.Encode(r))
	}
}

func cliPageRecord(url, title, body string) models.PageJSONL {
	return models.PageJSONL{
		RecordType:  models.RecordTypePage,
		URL:         url,
		Title:       title,
		Content:     body,
		ContentHash: utils.CalculateStringSHA256(body),
		CrawledAt:   "2026-01-01T00:00:00Z",
	}
}

// TestDoVerify_SiteNotInConfig covers the site-in-config precondition.
func TestDoVerify_SiteNotInConfig(t *testing.T) {
	f := newCLIVerifyFixture(t)
	var stdout, stderr bytes.Buffer
	code := doVerify(verifyArgs{configFile: f.cfgPath, siteKey: "unknown"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "not found in config")
}

// TestDoVerify_MissingOutputDirFailsUpFront ensures a missing output
// directory fails before any mutation.
func TestDoVerify_MissingOutputDirFailsUpFront(t *testing.T) {
	f := newCLIVerifyFixture(t)
	require.NoError(t, os.RemoveAll(f.outDir))
	var stdout, stderr bytes.Buffer
	code := doVerify(verifyArgs{configFile: f.cfgPath, siteKey: f.siteKey}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "output directory does not exist")
}

// TestDoVerify_CleanCorpusExitZero covers the "no issues" outcome in both
// report formats. A fixing run first materializes the llms files and search
// index so the subsequent read-only run sees a fully consistent corpus.
func TestDoVerify_CleanCorpusExitZero(t *testing.T) {
	f := newCLIVerifyFixture(t)
	body := "# Home\n"
	f.writePage(t, "index.md", "https://docs.example.com/", "Home", body, 0)
	f.writeManifest(t, []models.PageJSONL{cliPageRecord("https://docs.example.com/", "Home", body)}, 1)

	// One-time repair to create llms.txt, llms-full.txt and the index.
	var fixStderr bytes.Buffer
	require.Equal(t, 0, doVerify(verifyArgs{
		configFile: f.cfgPath, siteKey: f.siteKey, fix: true,
	}, &bytes.Buffer{}, &fixStderr), "fix stderr=%s", fixStderr.String())

	for _, jsonOut := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		code := doVerify(verifyArgs{
			configFile: f.cfgPath, siteKey: f.siteKey, jsonOut: jsonOut,
		}, &stdout, &stderr)
		assert.Equal(t, 0, code, "json=%v stderr=%s", jsonOut, stderr.String())
		if jsonOut {
			var report crawler.VerifyReport
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
			assert.Equal(t, 0, report.FindingsCount)
		} else {
			assert.Contains(t, stdout.String(), "No inconsistencies found.")
		}
	}
}

// TestDoVerify_FindsIssuesExit4 proves read-only runs report but never mutate.
func TestDoVerify_FindsIssuesExit4(t *testing.T) {
	f := newCLIVerifyFixture(t)
	// Manifest references a page whose Markdown file is absent.
	f.writeManifest(t, []models.PageJSONL{
		cliPageRecord("https://docs.example.com/gone/", "Gone", "# Gone\n"),
	}, 1)

	var stdout, stderr bytes.Buffer
	code := doVerify(verifyArgs{
		configFile: f.cfgPath, siteKey: f.siteKey,
	}, &stdout, &stderr)
	assert.Equal(t, exitVerifyIssues, code)
	assert.Contains(t, stdout.String(), crawler.IssueMissingMarkdown)
	assert.Contains(t, stdout.String(), "gone/index.md")

	// Read-only: no repair artifacts appeared.
	_, err := os.Stat(filepath.Join(f.outDir, "llms.txt"))
	assert.True(t, os.IsNotExist(err))
}

// TestDoVerify_FixClearsRepairableAndNamesResidual covers exit 5: the fix
// repairs what it can, while a wholly missing page stays residual.
func TestDoVerify_FixClearsRepairableAndNamesResidual(t *testing.T) {
	f := newCLIVerifyFixture(t)
	urlKeep := "https://docs.example.com/keep/"
	urlGone := "https://docs.example.com/gone/"
	urlOrphan := "https://docs.example.com/orphan/"

	// keep: edited on disk so its manifest hash is stale (repairable).
	f.writePage(t, "keep/index.md", urlKeep, "Keep", "# edited body\n", 0)
	// orphan: Markdown without a manifest record (repairable).
	f.writePage(t, "orphan/index.md", urlOrphan, "Orphan", "# orphan body\n", 0)
	// gone: manifest record only, no Markdown (unrepairable).
	f.writeManifest(t, []models.PageJSONL{
		cliPageRecord(urlKeep, "Keep", "# original body\n"),
		cliPageRecord(urlGone, "Gone", "# Gone\n"),
	}, 2)

	var stdout, stderr bytes.Buffer
	code := doVerify(verifyArgs{
		configFile: f.cfgPath, siteKey: f.siteKey, fix: true, jsonOut: true,
	}, &stdout, &stderr)
	require.Equal(t, exitVerifyResidual, code, "stderr=%s", stderr.String())

	var report crawler.VerifyReport
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	assert.True(t, report.Fixed)
	assert.NotEmpty(t, report.Fixes)
	assert.Greater(t, report.ResidualCount, 0)

	residualCats := map[string]bool{}
	for _, g := range report.Residual {
		residualCats[g.Category] = true
		for _, e := range g.Entries {
			assert.NotEmpty(t, e.URL)
		}
	}
	assert.True(t, residualCats[crawler.IssueMissingMarkdown])

	// llms files were regenerated and now follow the repaired manifest.
	llms, err := os.ReadFile(filepath.Join(f.outDir, "llms.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(llms), urlKeep)
	assert.Contains(t, string(llms), urlOrphan)
	assert.NotContains(t, string(llms), urlGone)

	// A second read-only verify reports the same now-clean artifact set
	// (the missing page's record was removed), proving convergence.
	var stdout2, stderr2 bytes.Buffer
	code2 := doVerify(verifyArgs{
		configFile: f.cfgPath, siteKey: f.siteKey, jsonOut: true,
	}, &stdout2, &stderr2)
	var after crawler.VerifyReport
	require.NoError(t, json.Unmarshal(stdout2.Bytes(), &after))
	assert.Equal(t, 0, after.FindingsCount, "stderr=%s report=%s", stderr2.String(), stdout2.String())
	assert.Equal(t, 0, code2)
}

// TestDoVerify_ConfigLoadFailure keeps ordinary config errors at exit 1.
func TestDoVerify_ConfigLoadFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doVerify(verifyArgs{
		configFile: filepath.Join(t.TempDir(), "missing.yaml"),
		siteKey:    "x",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error")
}

// keep the config import meaningful even if struct literals change.
var _ = config.LLMsTxtFilename
