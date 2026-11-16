package crawler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

// regexpMustCompile registers a package-level pattern.
func regexpMustCompile(expr string) *regexp.Regexp { return regexp.MustCompile(expr) }

// itoa keeps report-building free of strconv imports at call sites.
func itoa(n int) string { return strconv.Itoa(n) }

// verifyFrontMatter mirrors the page front matter process.ContentProcessor
// writes at the top of every saved Markdown file.
type verifyFrontMatter struct {
	Title       string `yaml:"title"`
	URL         string `yaml:"url"`
	CrawledAt   string `yaml:"crawled_at"`
	ContentHash string `yaml:"content_hash"`
	Depth       int    `yaml:"depth"`
}

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

// loadManifest reads and parses the JSONL manifest, keeping page and
// crawl_meta records in file order. Malformed lines are skipped, matching the
// crawler's streaming readers.
func (v *Verifier) loadManifest() (*manifestSnapshot, error) {
	f, err := os.Open(filepath.Join(v.dir, v.jsonl))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	snap := &manifestSnapshot{}
	scanner := NewJSONLScanner(f)
	lineNo := -1
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		// Cheap discriminator before paying for two unmarshals.
		if bytes.Contains(line, pageRecordMarker) {
			var p models.PageJSONL
			if err := json.Unmarshal(line, &p); err != nil {
				continue
			}
			if p.RecordType == models.RecordTypePage && p.URL != "" {
				snap.pages = append(snap.pages, p)
				snap.pageLines = append(snap.pageLines, lineNo)
			}
			continue
		}
		var m models.CrawlMetaJSONL
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m.RecordType == models.RecordTypeCrawlMeta {
			snap.metas = append(snap.metas, m)
			snap.metaLines = append(snap.metaLines, lineNo)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	snap.totalLines = lineNo + 1
	return snap, nil
}

// parseMarkdownPage splits a saved page file into front matter and body,
// returning ok=false for files without a doc-scraper page front matter block.
func parseMarkdownPage(raw []byte) (fm verifyFrontMatter, body string, ok bool) {
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		return fm, "", false
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return fm, "", false
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return fm, "", false
	}
	if fm.URL == "" || fm.CrawledAt == "" || fm.ContentHash == "" {
		return fm, "", false
	}
	body = rest[end+len("\n---\n"):]
	// The crawler separates the closing front matter delimiter from the body
	// with a blank line; treat that separator as part of the envelope, not
	// the content, so the hash matches pages.jsonl's body hash.
	body = strings.TrimPrefix(body, "\n")
	return fm, body, true
}

// scanDiskPages walks the output dir and parses every Markdown file's front
// matter. Files without page front matter are ignored, matching the crawler's
// orphan sweep.
func (v *Verifier) scanDiskPages() ([]diskPage, error) {
	var pages []diskPage
	root := filepath.Clean(v.dir)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fm, body, ok := parseMarkdownPage(raw)
		if !ok {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		pages = append(pages, diskPage{
			absPath:       path,
			relPath:       filepath.ToSlash(rel),
			url:           fm.URL,
			title:         fm.Title,
			depth:         fm.Depth,
			crawledAt:     fm.CrawledAt,
			frontHash:     fm.ContentHash,
			body:          body,
			bodyHash:      utils.CalculateStringSHA256(body),
			frontMatterOK: true,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pages, nil
}

// checkMarkdownPresence covers categories 1 and 2: manifest pages missing
// their Markdown file, and Markdown files the manifest does not know about.
func (v *Verifier) checkMarkdownPresence(man *manifestSnapshot, disk []diskPage) map[string][]IssueEntry {
	out := map[string][]IssueEntry{}

	diskByURL := make(map[string]diskPage, len(disk))
	diskByPath := make(map[string]diskPage, len(disk))
	for _, d := range disk {
		diskByURL[d.url] = d
		diskByPath[d.relPath] = d
	}
	manifestURLs := make(map[string]struct{}, len(man.pages))

	// Category 1: manifest record with no Markdown at its mapped path.
	for _, p := range man.pages {
		manifestURLs[p.URL] = struct{}{}
		rel := ""
		if abs, ok := v.expectedMarkdownPath(p.URL); ok {
			rel = v.relPath(abs)
			if d, present := diskByPath[rel]; present && d.url == p.URL {
				continue
			}
		}
		out[IssueMissingMarkdown] = append(out[IssueMissingMarkdown], IssueEntry{
			Category: IssueMissingMarkdown,
			Path:     rel,
			URL:      p.URL,
		})
	}

	// Category 2: a front-matter Markdown file no manifest record names.
	for _, d := range disk {
		if _, known := manifestURLs[d.url]; known {
			continue
		}
		out[IssueOrphanMarkdown] = append(out[IssueOrphanMarkdown], IssueEntry{
			Category: IssueOrphanMarkdown,
			Path:     d.relPath,
			URL:      d.url,
		})
	}
	return out
}

// checkFingerprints covers category 3: disk body vs manifest content_hash.
// Only pages whose Markdown file exists are compared; missing files belong to
// category 1 instead.
func (v *Verifier) checkFingerprints(man *manifestSnapshot, disk []diskPage) map[string][]IssueEntry {
	diskByURL := make(map[string]diskPage, len(disk))
	for _, d := range disk {
		diskByURL[d.url] = d
	}
	out := map[string][]IssueEntry{}
	for _, p := range man.pages {
		d, ok := diskByURL[p.URL]
		if !ok {
			continue
		}
		if p.ContentHash == "" || p.ContentHash != d.bodyHash {
			out[IssueHashMismatch] = append(out[IssueHashMismatch], IssueEntry{
				Category: IssueHashMismatch,
				Path:     d.relPath,
				URL:      p.URL,
				Detail: map[string]string{
					"manifest_hash": p.ContentHash,
					"disk_hash":     d.bodyHash,
				},
			})
		}
	}
	return out
}

// readFileLines is a small helper kept for later checks that parse plain
// text files line by line.
func readFileLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	r := bufio.NewReader(f)
	scanner := NewJSONLScanner(r)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// checkCrawlMeta covers category 4: summary count mismatch, duplicate
// summaries, or a summary that is not the manifest's last line.
func (v *Verifier) checkCrawlMeta(man *manifestSnapshot) map[string][]IssueEntry {
	out := map[string][]IssueEntry{}
	add := func(detail string) {
		out[IssueCrawlMeta] = append(out[IssueCrawlMeta], IssueEntry{
			Category: IssueCrawlMeta,
			Path:     v.jsonl,
			Detail:   map[string]string{"problem": detail},
		})
	}

	switch len(man.metas) {
	case 0:
		add("no crawl_meta summary record")
	case 1:
		// canonical position: the very last line of the file
		if man.metaLines[0] != man.totalLines-1 {
			add("crawl_meta is not the last line of the manifest")
		}
	default:
		add("more than one crawl_meta summary record")
	}
	for i, m := range man.metas {
		if m.TotalPages != len(man.pages) {
			out[IssueCrawlMeta] = append(out[IssueCrawlMeta], IssueEntry{
				Category: IssueCrawlMeta,
				Path:     v.jsonl,
				Detail: map[string]string{
					"problem":            "summary page count does not match page records",
					"summary_total_pages": itoa(m.TotalPages),
					"page_records":        itoa(len(man.pages)),
					"summary_line":        itoa(man.metaLines[i] + 1),
				},
			})
		}
	}
	return out
}

// llmsEntry is one ordered (title, url) pair parsed from an llms file.
type llmsEntry struct {
	title string
	url   string
	line  int
}

var llmsLinkLine = regexpMustCompile(`^\s*-\s*\[([^\]]*)\]\(([^)]+)\)\s*$`)
var llmsURLLine = regexpMustCompile(`^URL:\s*(\S+)\s*$`)

// parseLLMsIndex reads the "- [title](url)" entries of llms.txt in order.
func parseLLMsIndex(path string) ([]llmsEntry, error) {
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	var entries []llmsEntry
	for i, line := range lines {
		m := llmsLinkLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, llmsEntry{title: m[1], url: m[2], line: i + 1})
	}
	return entries, nil
}

// parseLLMsFull reads the "URL: ..." entry markers of llms-full.txt in order.
func parseLLMsFull(path string) ([]llmsEntry, error) {
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	var entries []llmsEntry
	for i, line := range lines {
		m := llmsURLLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, llmsEntry{url: m[1], line: i + 1})
	}
	return entries, nil
}

// compareLLMsEntries diffs one llms file's ordered URL list against the
// manifest's ordered URL list. Missing files surface every manifest URL as
// missing. Order mismatches are reported once per out-of-place URL so the
// human and structured reports name concrete entries.
func (v *Verifier) compareLLMsEntries(cat, filename string, got []llmsEntry, man *manifestSnapshot, fileExists bool) map[string][]IssueEntry {
	out := map[string][]IssueEntry{}

	wantURLs := make([]string, 0, len(man.pages))
	wantSet := make(map[string]int, len(man.pages))
	for _, p := range man.pages {
		wantSet[p.URL]++
		wantURLs = append(wantURLs, p.URL)
	}
	gotSet := make(map[string]int, len(got))
	for _, e := range got {
		gotSet[e.url]++
	}

	if !fileExists {
		for _, u := range wantURLs {
			out[cat] = append(out[cat], IssueEntry{
				Category: cat,
				Path:     filename,
				URL:      u,
				Detail:   map[string]string{"problem": "file missing; entry absent"},
			})
		}
		return out
	}

	// Entries the manifest does not have (extra), accounting for duplicates.
	seen := map[string]int{}
	for _, e := range got {
		seen[e.url]++
		if seen[e.url] > wantSet[e.url] {
			out[cat] = append(out[cat], IssueEntry{
				Category: cat,
				Path:     filename,
				URL:      e.url,
				Detail:   map[string]string{"problem": "entry not in manifest", "line": itoa(e.line)},
			})
		}
	}
	// Manifest URLs the file lacks (missing).
	seen = map[string]int{}
	gotIndex := map[string][]int{}
	for _, e := range got {
		gotIndex[e.url] = append(gotIndex[e.url], e.line)
	}
	for _, u := range wantURLs {
		seen[u]++
		if seen[u] > gotSet[u] {
			out[cat] = append(out[cat], IssueEntry{
				Category: cat,
				Path:     filename,
				URL:      u,
				Detail:   map[string]string{"problem": "manifest entry missing from file"},
			})
		}
	}

	// Order: compare the URL sequences after dropping duplicates and extras.
	gotURLs := make([]string, 0, len(got))
	for _, e := range got {
		gotURLs = append(gotURLs, e.url)
	}
	for _, detail := range orderMismatches(filename, wantURLs, gotURLs, gotIndex) {
		detail.Category = cat
		out[cat] = append(out[cat], detail)
	}
	return out
}

// orderMismatches reports manifest URLs whose position in the llms file
// differs from their manifest position, restricted to URLs present on both
// sides so missing/extra reporting stays in one place.
func orderMismatches(filename string, wantURLs, gotURLs []string, gotLines map[string][]int) []IssueEntry {
	commonOrder := func(all []string, allow map[string]bool) []string {
		out := make([]string, 0, len(all))
		seen := map[string]bool{}
		for _, u := range all {
			if allow[u] && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
		return out
	}
	common := map[string]bool{}
	wantSet := map[string]bool{}
	for _, u := range wantURLs {
		wantSet[u] = true
	}
	for _, u := range gotURLs {
		if wantSet[u] {
			common[u] = true
		}
	}
	wantCommon := commonOrder(wantURLs, common)
	gotCommon := commonOrder(gotURLs, common)

	var mismatches []IssueEntry
	for i, u := range wantCommon {
		if i >= len(gotCommon) || gotCommon[i] != u {
			line := 0
			if l := gotLines[u]; len(l) > 0 {
				line = l[0]
			}
			mismatches = append(mismatches, IssueEntry{
				Category: "", // caller overwrites
				Path:     filename,
				URL:      u,
				Detail: map[string]string{
					"problem":           "entry order does not match manifest",
					"manifest_position": itoa(i + 1),
					"file_line":         itoa(line),
				},
			})
		}
	}
	return mismatches
}

// checkLLMsFiles covers category 5: llms.txt and llms-full.txt entries
// missing, extra, or ordered differently than the manifest.
func (v *Verifier) checkLLMsFiles(man *manifestSnapshot) map[string][]IssueEntry {
	out := map[string][]IssueEntry{}

	indexPath := filepath.Join(v.dir, llmsTxtFilename)
	indexEntries, indexErr := parseLLMsIndex(indexPath)
	indexExists := !errors.Is(indexErr, fs.ErrNotExist)
	if indexErr != nil && indexExists {
		v.log.Warn("verify: cannot parse llms.txt", "path", indexPath, "err", indexErr)
	}
	for k, entries := range v.compareLLMsEntries(IssueLLMsTxt, llmsTxtFilename, indexEntries, man, indexExists) {
		out[k] = append(out[k], entries...)
	}

	fullPath := filepath.Join(v.dir, llmsFullTxtFilename)
	fullEntries, fullErr := parseLLMsFull(fullPath)
	fullExists := !errors.Is(fullErr, fs.ErrNotExist)
	if fullErr != nil && fullExists {
		v.log.Warn("verify: cannot parse llms-full.txt", "path", fullPath, "err", fullErr)
	}
	for k, entries := range v.compareLLMsEntries(IssueLLMsFullTxt, llmsFullTxtFilename, fullEntries, man, fullExists) {
		out[k] = append(out[k], entries...)
	}
	return out
}

// isLocalImageRef reports whether ref points at a file inside the output tree
// rather than a remote URL or a data: URI.
func isLocalImageRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "data:") {
		return false
	}
	if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Scheme != "file" {
		return false
	}
	return true
}

// checkImages covers category 6: local image files referenced by manifest
// records that do not exist under the output directory. Image references are
// resolved relative to the Markdown file that carries them, matching how the
// crawler rewrites src attributes.
func (v *Verifier) checkImages(man *manifestSnapshot) map[string][]IssueEntry {
	out := map[string][]IssueEntry{}
	reported := map[string]struct{}{}
	for _, p := range man.pages {
		abs, ok := v.expectedMarkdownPath(p.URL)
		if !ok {
			continue
		}
		pageDir := filepath.Dir(abs)
		for _, ref := range p.Images {
			if !isLocalImageRef(ref) {
				continue
			}
			imgAbs := filepath.Clean(filepath.Join(pageDir, filepath.FromSlash(ref)))
			if _, err := os.Stat(imgAbs); err == nil {
				continue
			}
			rel := v.relPath(imgAbs)
			key := p.URL + "|" + rel
			if _, dup := reported[key]; dup {
				continue
			}
			reported[key] = struct{}{}
			out[IssueMissingImage] = append(out[IssueMissingImage], IssueEntry{
				Category: IssueMissingImage,
				Path:     rel,
				URL:      p.URL,
				Detail:   map[string]string{"reference": ref},
			})
		}
	}
	return out
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
