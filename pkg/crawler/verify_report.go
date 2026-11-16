package crawler

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// RenderVerifyHuman writes the human-readable form of a VerifyReport to w.
// Counts and item lists match the structured report exactly: one line per
// concrete page or file, with its output-dir-relative path and URL.
func RenderVerifyHuman(w io.Writer, r *VerifyReport) {
	renderHumanReport(w, r)
}

// RenderVerifyJSON writes the machine-readable form of r to w.
func RenderVerifyJSON(w io.Writer, r *VerifyReport) error {
	return renderJSONReport(w, r)
}

// renderHumanReport writes the human-readable form of a VerifyReport to w.
func renderHumanReport(w io.Writer, r *VerifyReport) {
}

// renderJSONReport writes the machine-readable form of r to w.
func renderJSONReport(w io.Writer, r *VerifyReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// groupFindings orders issue groups by category and sorts each group's
// entries so both report forms and repeated runs are deterministic.
func groupFindings(byCat map[string][]IssueEntry) []IssueGroup {
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	groups := make([]IssueGroup, 0, len(cats))
	for _, c := range cats {
		entries := byCat[c]
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Path != entries[j].Path {
				return entries[i].Path < entries[j].Path
			}
			if entries[i].URL != entries[j].URL {
				return entries[i].URL < entries[j].URL
			}
			return entries[i].Category < entries[j].Category
		})
		groups = append(groups, IssueGroup{Category: c, Count: len(entries), Entries: entries})
	}
	return groups
}

// countEntries sums a report's group counts.
func countEntries(groups []IssueGroup) int {
	n := 0
	for _, g := range groups {
		n += g.Count
	}
	return n
}

// relPath makes p relative to the output dir for reports.
func (v *Verifier) relPath(p string) string {
	rel, err := filepath.Rel(v.dir, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

// humanIssueTitles maps each issue category to a human-readable heading.
var humanIssueTitles = map[string]string{
	IssueMissingMarkdown:  "Manifest pages with no Markdown file on disk",
	IssueOrphanMarkdown:   "Markdown files on disk with no manifest record",
	IssueHashMismatch:     "Pages whose disk content does not match the manifest fingerprint",
	IssueCrawlMeta:        "Crawl summary (crawl_meta) problems",
	IssueLLMsTxt:          "llms.txt entries out of sync with the manifest",
	IssueLLMsFullTxt:      "llms-full.txt entries out of sync with the manifest",
	IssueMissingImage:     "Manifest-referenced local image files missing on disk",
	IssueIndexMissingPage: "Pages in the manifest but absent from the search index",
	IssueIndexStalePage:   "Pages in the search index but absent from the manifest",
}

// formatEntry renders one issue line with its relative path and URL.
func formatEntry(e IssueEntry) string {
	var b strings.Builder
	if e.Path != "" {
		b.WriteString(e.Path)
	}
	if e.URL != "" {
		if b.Len() > 0 {
			b.WriteString("  ")
		}
		b.WriteString(e.URL)
	}
	for _, k := range sortedDetailKeys(e.Detail) {
		b.WriteString(fmt.Sprintf("  %s=%s", k, e.Detail[k]))
	}
	if b.Len() == 0 {
		b.WriteString("(no location)")
	}
	return b.String()
}

func sortedDetailKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
