package crawler

import (
	"context"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
)

// repairPlan describes what a fixing run will change. It is computed from the
// initial findings before anything is written, so precondition failures never
// leave a half-repaired corpus.
type repairPlan struct {
	// orphanRecords holds rebuilt manifest records for Markdown files the
	// manifest did not know about.
	orphanRecords []rebuiltRecord
	// hashUpdates maps page URL to its disk-derived content hash for pages
	// whose body was edited on disk.
	hashUpdates map[string]string
	// removedURLs lists manifest records with no Markdown file on disk.
	removedURLs []string
	rewriteJSONL bool
	rewriteLLMs  bool
	rebuildIndex bool
}

// rebuiltRecord is a manifest record reconstructed entirely from local
// content (a Markdown file's front matter and body).
type rebuiltRecord struct {
	url       string
	title     string
	body      string
	depth     int
	crawledAt string
}

// planRepair turns findings plus local content into a repair plan. Implemented
// in a later block.
func (v *Verifier) planRepair(man *manifestSnapshot, disk []diskPage, findings map[string][]IssueEntry) *repairPlan {
	return &repairPlan{hashUpdates: map[string]string{}}
}

// applyRepair executes the plan: rewrite the manifest, rewrite llms.txt and
// llms-full.txt, and rebuild the search index. Implemented in a later block.
func (v *Verifier) applyRepair(ctx context.Context, plan *repairPlan, man *manifestSnapshot, idx *index.Index) ([]FixEntry, error) {
	return nil, nil
}

// rewriteManifest writes the canonicalized manifest (URL-sorted page records
// plus one final crawl_meta) atomically. Implemented in a later block.
func (v *Verifier) rewriteManifest(man *manifestSnapshot) error {
	return nil
}

// writeLLMsFiles rewrites llms.txt and llms-full.txt from the manifest using
// the exact format the crawler emits. Implemented in a later block.
func (v *Verifier) writeLLMsFiles(man *manifestSnapshot) error {
	return nil
}

// rebuildSearchIndex replaces the site's chunks from the manifest. Implemented
// in a later block.
func (v *Verifier) rebuildSearchIndex(ctx context.Context, idx *index.Index, man *manifestSnapshot) error {
	return nil
}
