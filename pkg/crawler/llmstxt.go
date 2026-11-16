package crawler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/config"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/models"
)

const (
	llmsTxtFilename     = config.LLMsTxtFilename
	llmsFullTxtFilename = config.LLMsFullTxtFilename
)

// writeLLMsTxtFiles emits llms.txt and llms-full.txt per https://llmstxt.org/,
// streaming from the already-flushed JSONL so memory stays bounded for large
// crawls. No-op if JSONL is disabled or unreadable.
func (om *OutputManager) writeLLMsTxtFiles() {
	if om.jsonlFilePath == "" || om.siteOutputDir == "" {
		return
	}
	if err := WriteLLMsFiles(om.jsonlFilePath, om.siteOutputDir, om.siteKey, domainOf(om.siteCfg), om.log); err != nil {
		om.log.Warn(fmt.Sprintf("llms.txt: %v", err))
	}
}

// domainOf returns the configured allowed domain, tolerating a nil config.
func domainOf(siteCfg *config.SiteConfig) string {
	if siteCfg == nil {
		return ""
	}
	return siteCfg.AllowedDomain
}

// WriteLLMsFiles regenerates llms.txt and llms-full.txt from a finalized
// JSONL manifest, using the exact on-disk format a crawl emits. Shared by
// crawl finalization and offline verify --fix so the two paths cannot drift.
func WriteLLMsFiles(jsonlPath, outDir, siteKey, domain string, log *slog.Logger) error {
	in, err := os.Open(jsonlPath)
	if err != nil {
		return fmt.Errorf("cannot read JSONL %s: %w", jsonlPath, err)
	}
	defer in.Close()

	fullPath := filepath.Join(outDir, llmsFullTxtFilename)
	fullFile, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", fullPath, err)
	}
	defer fullFile.Close()

	fmt.Fprintf(fullFile, "# %s\n\n> Full crawled content from %s.\n\n", siteKey, domain)

	type pageRef struct{ title, url string }
	var pages []pageRef

	scanner := NewJSONLScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Cheap pre-filter to avoid Unmarshal cost on non-page records.
		if !strings.Contains(line, `"record_type":"page"`) {
			continue
		}
		var page models.PageJSONL
		if err := json.Unmarshal([]byte(line), &page); err != nil {
			continue
		}
		if page.RecordType != models.RecordTypePage {
			continue
		}

		title := strings.TrimSpace(page.Title)
		if title == "" {
			title = page.URL
		}
		pages = append(pages, pageRef{title: title, url: page.URL})

		fmt.Fprintf(fullFile, "---\n\n# %s\n\nURL: %s\n\n%s\n\n", escapeHeading(title), page.URL, page.Content)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan error on %s: %w", jsonlPath, err)
	}

	indexPath := filepath.Join(outDir, llmsTxtFilename)
	indexFile, err := os.Create(indexPath)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", indexPath, err)
	}
	defer indexFile.Close()

	fmt.Fprintf(indexFile, "# %s\n\n", siteKey)
	fmt.Fprintf(indexFile, "> Crawled documentation from %s. %d pages.\n\n", domain, len(pages))
	fmt.Fprintln(indexFile, "## Pages")
	if len(pages) == 0 {
		fmt.Fprintln(indexFile, "_No pages were successfully crawled._")
	}
	for _, p := range pages {
		fmt.Fprintf(indexFile, "- [%s](%s)\n", escapeLinkText(p.title), p.url)
	}

	if log != nil {
		log.Info(fmt.Sprintf("Wrote llms.txt (%d pages) and llms-full.txt to %s", len(pages), outDir))
	}
	return nil
}

// escapeLinkText escapes brackets, backslashes, and newlines in markdown link
// text. URLs are assumed well-formed and are not escaped.
func escapeLinkText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `[`, `\[`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// escapeHeading collapses embedded newlines so a multi-line title stays on one H1.
func escapeHeading(s string) string {
	return strings.ReplaceAll(s, "\n", " ")
}
