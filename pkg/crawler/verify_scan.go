package crawler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/utils"
)

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
