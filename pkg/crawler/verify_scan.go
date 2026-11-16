package crawler

import (
	"context"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
)

// manifestSnapshot is the parsed view of pages.jsonl: page records in file
// order, every crawl_meta record in file order, and the 0-based line numbers
// of the meta records so "meta not on the last line" is checkable.
type manifestSnapshot struct {
	pages      []models.PageJSONL
	pageLines  []int
	metas      []models.CrawlMetaJSONL
	metaLines  []int
	totalLines int
}

// diskPage is one Markdown file carrying doc-scraper page front matter.
type diskPage struct {
	absPath       string
	relPath       string
	url           string
	title         string
	depth         int
	crawledAt     string
	frontHash     string
	body          string
	bodyHash      string
	frontMatterOK bool
}

// loadManifest reads and parses the JSONL manifest. Implemented in a later
// block.
func (v *Verifier) loadManifest() (*manifestSnapshot, error) {
	return nil, nil
}

// scanDiskPages walks the output dir and parses every Markdown file's front
// matter. Files without page front matter are ignored, matching the crawler's
// orphan sweep. Implemented in a later block.
func (v *Verifier) scanDiskPages() ([]diskPage, error) {
	return nil, nil
}

// checkMarkdownPresence covers categories 1 and 2: manifest pages missing
// their Markdown file, and Markdown files the manifest does not know about.
func (v *Verifier) checkMarkdownPresence(man *manifestSnapshot, disk []diskPage) map[string][]IssueEntry {
	return map[string][]IssueEntry{}
}

// checkFingerprints covers category 3: disk body vs manifest content_hash.
func (v *Verifier) checkFingerprints(man *manifestSnapshot, disk []diskPage) map[string][]IssueEntry {
	return map[string][]IssueEntry{}
}

// checkCrawlMeta covers category 4: summary count mismatch, duplicate
// summaries, or a summary that is not the manifest's last line.
func (v *Verifier) checkCrawlMeta(man *manifestSnapshot) map[string][]IssueEntry {
	return map[string][]IssueEntry{}
}

// checkLLMsFiles covers category 5: llms.txt and llms-full.txt entries
// missing, extra, or ordered differently than the manifest.
func (v *Verifier) checkLLMsFiles(man *manifestSnapshot) map[string][]IssueEntry {
	return map[string][]IssueEntry{}
}

// checkImages covers category 6: local image files referenced by manifest
// records that do not exist under the output directory.
func (v *Verifier) checkImages(man *manifestSnapshot) map[string][]IssueEntry {
	return map[string][]IssueEntry{}
}

// checkIndex covers category 7: pages missing from or stale in the search
// index. An unreadable index is reported through the returned error.
func (v *Verifier) checkIndex(ctx context.Context, man *manifestSnapshot, idx *index.Index) map[string][]IssueEntry {
	return map[string][]IssueEntry{}
}

// mergeIssueMaps folds src into dst.
func mergeIssueMaps(dst, src map[string][]IssueEntry) {
	for cat, entries := range src {
		dst[cat] = append(dst[cat], entries...)
	}
}
