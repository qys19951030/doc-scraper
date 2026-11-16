package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/chunk"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/process"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

// repairPlan describes what a fixing run will change. It is computed from the
// initial findings before anything is written, so precondition failures never
// leave a half-repaired corpus.
type repairPlan struct {
	// orphanRecords holds rebuilt manifest records for Markdown files the
	// manifest did not know about.
	orphanRecords []models.PageJSONL
	// hashUpdates maps page URL to its disk-derived content hash for pages
	// whose body was edited on disk.
	hashUpdates map[string]hashUpdate
	// removedURLs lists manifest records with no Markdown file on disk.
	removedURLs []string
	rewriteJSONL bool
	rewriteLLMs  bool
	rebuildIndex bool
}

// hashUpdate keeps both hashes for the report: the stale manifest hash and
// the hash computed from the disk body.
type hashUpdate struct {
	oldHash string
	newHash string
	relPath string
}

// planRepair turns findings plus local content into a repair plan. Everything
// repairable is derivable from the Markdown files on disk; a page whose file
// is gone is only removed from the manifest, never refilled.
func (v *Verifier) planRepair(man *manifestSnapshot, disk []diskPage, findings map[string][]IssueEntry) *repairPlan {
	plan := &repairPlan{hashUpdates: map[string]hashUpdate{}}

	diskByURL := map[string]diskPage{}
	for _, d := range disk {
		diskByURL[d.url] = d
	}

	// Category 2: rebuild records for orphaned Markdown files.
	for _, e := range findings[IssueOrphanMarkdown] {
		d, ok := diskByURL[e.URL]
		if !ok {
			continue
		}
		plan.orphanRecords = append(plan.orphanRecords, v.recordFromDisk(d))
		plan.rewriteJSONL = true
	}

	// Category 3: keep the disk body, adopt its hash and rebuilt derived
	// fields, remembering the original manifest hash for the report.
	for _, e := range findings[IssueHashMismatch] {
		d, ok := diskByURL[e.URL]
		if !ok {
			continue
		}
		plan.hashUpdates[e.URL] = hashUpdate{
			oldHash: e.Detail["manifest_hash"],
			newHash: d.bodyHash,
			relPath: d.relPath,
		}
		plan.rewriteJSONL = true
	}

	// Category 1: drop records whose Markdown cannot be rebuilt locally.
	for _, e := range findings[IssueMissingMarkdown] {
		if _, onDisk := diskByURL[e.URL]; onDisk {
			continue
		}
		plan.removedURLs = append(plan.removedURLs, e.URL)
		plan.rewriteJSONL = true
	}

	// Categories 4 and 5 are always made consistent by rewriting: the
	// canonical manifest write fixes crawl_meta, and the llms files are
	// regenerated from it.
	if len(findings[IssueCrawlMeta]) > 0 {
		plan.rewriteJSONL = true
	}
	if len(findings[IssueLLMsTxt]) > 0 || len(findings[IssueLLMsFullTxt]) > 0 {
		plan.rewriteLLMs = true
	}
	// Category 7: rebuild the search index from the post-repair manifest.
	if len(findings[IssueIndexMissingPage]) > 0 || len(findings[IssueIndexStalePage]) > 0 {
		plan.rebuildIndex = true
	}
	// A manifest rewrite always cascades into llms and index rewrites so all
	// artifacts describe the same page set.
	if plan.rewriteJSONL {
		plan.rewriteLLMs = true
		plan.rebuildIndex = true
	}
	return plan
}

// recordFromDisk reconstructs a manifest page record from a Markdown file's
// front matter and body.
func (v *Verifier) recordFromDisk(d diskPage) models.PageJSONL {
	bodyBytes := []byte(d.body)
	links, images := extractLinksAndImages(d.body)
	return models.PageJSONL{
		RecordType:  models.RecordTypePage,
		URL:         d.url,
		Title:       d.title,
		Content:     d.body,
		Headings:    process.ExtractHeadings(bodyBytes),
		Links:       links,
		Images:      images,
		ContentHash: d.bodyHash,
		CrawledAt:   d.crawledAt,
		Depth:       d.depth,
	}
}

// rebuildManifestRecords applies the plan to the parsed manifest, returning
// URL-sorted, deduplicated page records and a fresh crawl_meta.
func (v *Verifier) rebuildManifestRecords(man *manifestSnapshot, plan *repairPlan) ([]models.PageJSONL, models.CrawlMetaJSONL) {
	removed := map[string]struct{}{}
	for _, u := range plan.removedURLs {
		removed[u] = struct{}{}
	}
	orphans := map[string]models.PageJSONL{}
	for _, rec := range plan.orphanRecords {
		orphans[rec.URL] = rec
	}

	byURL := map[string]models.PageJSONL{}
	for _, p := range man.pages {
		if _, drop := removed[p.URL]; drop {
			continue
		}
		if upd, ok := plan.hashUpdates[p.URL]; ok {
			// The disk body wins; rebuild every field derived from it.
			p.ContentHash = upd.newHash
			// Reload the body from disk so content/headings/links/images match.
			p = v.applyDiskBody(p, upd.relPath)
		}
		if orphan, ok := orphans[p.URL]; ok {
			p = orphan
			delete(orphans, p.URL)
		}
		byURL[p.URL] = p
	}
	for _, rec := range orphans {
		byURL[rec.URL] = rec
	}

	urls := make([]string, 0, len(byURL))
	for u := range byURL {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	pages := make([]models.PageJSONL, 0, len(urls))
	for _, u := range urls {
		pages = append(pages, byURL[u])
	}

	meta := v.freshCrawlMeta(man, len(pages))
	return pages, meta
}

// applyDiskBody reloads a page's Markdown body from disk and rebuilds the
// derived manifest fields around it, preserving URL-level metadata.
func (v *Verifier) applyDiskBody(p models.PageJSONL, relPath string) models.PageJSONL {
	abs := filepath.Join(v.dir, filepath.FromSlash(relPath))
	raw, err := os.ReadFile(abs)
	if err != nil {
		return p
	}
	fm, body, ok := parseMarkdownPage(raw)
	if !ok {
		return p
	}
	bodyBytes := []byte(body)
	links, images := extractLinksAndImages(body)
	p.Title = fm.Title
	p.Content = body
	p.Headings = process.ExtractHeadings(bodyBytes)
	p.Links = links
	p.Images = images
	p.ContentHash = utils.CalculateStringSHA256(body)
	p.CrawledAt = fm.CrawledAt
	p.Depth = fm.Depth
	return p
}

// freshCrawlMeta carries the last summary's timing fields forward with a
// corrected page count and the verifier's site identity.
func (v *Verifier) freshCrawlMeta(man *manifestSnapshot, total int) models.CrawlMetaJSONL {
	meta := models.CrawlMetaJSONL{
		RecordType: models.RecordTypeCrawlMeta,
		SiteKey:    v.siteKey,
		TotalPages: total,
	}
	if len(man.metas) > 0 {
		last := man.metas[len(man.metas)-1]
		meta.AllowedDomain = last.AllowedDomain
		meta.CrawlStartedAt = last.CrawlStartedAt
		meta.CrawlEndedAt = last.CrawlEndedAt
	}
	if meta.AllowedDomain == "" && v.siteCfg != nil {
		meta.AllowedDomain = v.siteCfg.AllowedDomain
	}
	return meta
}

// rewriteManifest writes the canonicalized manifest (URL-sorted page records
// plus one final crawl_meta) atomically.
func (v *Verifier) rewriteManifest(pages []models.PageJSONL, meta models.CrawlMetaJSONL) error {
	target := filepath.Join(v.dir, v.jsonl)
	tmp := target + ".verify-tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for i := range pages {
		if err := enc.Encode(pages[i]); err != nil {
			return fmt.Errorf("write page record: %w", err)
		}
	}
	if err := enc.Encode(meta); err != nil {
		return fmt.Errorf("write crawl_meta: %w", err)
	}
	if err := f.Sync(); err != nil {
		v.log.Warn("verify: failed to sync rewritten manifest", "err", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp manifest: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	committed = true
	return nil
}

// writeLLMsFiles regenerates both llms files from the rewritten manifest.
func (v *Verifier) writeLLMsFiles() error {
	return WriteLLMsFiles(
		filepath.Join(v.dir, v.jsonl),
		v.dir,
		v.siteKey,
		domainOf(v.siteCfg),
		v.log,
	)
}

// rebuildSearchIndex replaces the site's chunks from the manifest so the
// index describes exactly the repaired page set with current hashes.
func (v *Verifier) rebuildSearchIndex(ctx context.Context, idx *index.Index, pages []models.PageJSONL) error {
	if idx == nil {
		return nil
	}
	if err := idx.DeleteSiteChunks(ctx, v.siteKey); err != nil {
		return fmt.Errorf("clear stale chunks: %w", err)
	}
	for _, p := range pages {
		if err := idx.ReplaceChunks(ctx, v.siteKey, p.URL, p.Title, p.ContentHash, chunk.Split(p.Content)); err != nil {
			return fmt.Errorf("index page %s: %w", p.URL, err)
		}
	}
	return nil
}

// applyRepair executes the plan end to end and reports each action.
func (v *Verifier) applyRepair(ctx context.Context, plan *repairPlan, man *manifestSnapshot, idx *index.Index) ([]FixEntry, error) {
	var fixes []FixEntry

	pages, meta := v.rebuildManifestRecords(man, plan)

	if plan.rewriteJSONL {
		if err := v.rewriteManifest(pages, meta); err != nil {
			return fixes, err
		}
		for _, rec := range plan.orphanRecords {
			fixes = append(fixes, FixEntry{
				Category: IssueOrphanMarkdown,
				Action:   "add_record",
				URL:      rec.URL,
				Path:     v.relPathForURL(rec.URL),
			})
		}
		for _, u := range plan.removedURLs {
			fixes = append(fixes, FixEntry{
				Category: IssueMissingMarkdown,
				Action:   "remove_record",
				URL:      u,
			})
		}
		for u, upd := range plan.hashUpdates {
			fixes = append(fixes, FixEntry{
				Category: IssueHashMismatch,
				Action:   "update_hash",
				URL:      u,
				Path:     upd.relPath,
				Detail:   map[string]string{"old_hash": upd.oldHash, "new_hash": upd.newHash},
			})
		}
		fixes = append(fixes, FixEntry{
			Category: IssueCrawlMeta,
			Action:   "rewrite_manifest",
			Path:     v.jsonl,
			Detail:   map[string]string{"total_pages": itoa(len(pages))},
		})
	}

	if plan.rewriteLLMs {
		if err := v.writeLLMsFiles(); err != nil {
			return fixes, err
		}
		fixes = append(fixes,
			FixEntry{Category: IssueLLMsTxt, Action: "rewrite_file", Path: llmsTxtFilename},
			FixEntry{Category: IssueLLMsFullTxt, Action: "rewrite_file", Path: llmsFullTxtFilename},
		)
	}

	if plan.rebuildIndex {
		if err := v.rebuildSearchIndex(ctx, idx, pages); err != nil {
			return fixes, err
		}
		fixes = append(fixes, FixEntry{
			Category: IssueIndexMissingPage,
			Action:   "rebuild_index",
			Path:     v.indexPath,
		})
	}

	sortFixes(fixes)
	return fixes, nil
}

// relPathForURL maps a URL to its output-dir-relative Markdown path for
// report lines, tolerating URLs the mapping rejects.
func (v *Verifier) relPathForURL(pageURL string) string {
	abs, ok := v.expectedMarkdownPath(pageURL)
	if !ok {
		return ""
	}
	return v.relPath(abs)
}

// sortFixes orders repair actions deterministically for stable reports.
func sortFixes(fixes []FixEntry) {
	sort.Slice(fixes, func(i, j int) bool {
		if fixes[i].Category != fixes[j].Category {
			return fixes[i].Category < fixes[j].Category
		}
		if fixes[i].URL != fixes[j].URL {
			return fixes[i].URL < fixes[j].URL
		}
		return fixes[i].Action < fixes[j].Action
	})
}
