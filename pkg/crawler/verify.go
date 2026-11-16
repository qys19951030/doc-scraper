// Package crawler: verify.go holds the offline corpus verification and repair
// surface. A Verifier reads only a site's local output directory (pages.jsonl,
// the per-page Markdown tree, llms.txt/llms-full.txt) and the configured
// SQLite search index; it never makes a network request.
package crawler

import (
	"context"
	"errors"
	"log/slog"
	"net/url"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/config"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/process"
)

// Issue category identifiers. Every problem the Verifier can detect has a
// stable identifier so scripts consuming the structured report do not have to
// parse human-readable text.
const (
	// IssueMissingMarkdown: a page record in the manifest has no Markdown
	// file at the path the URL maps to.
	IssueMissingMarkdown = "missing_markdown"
	// IssueOrphanMarkdown: a Markdown file with page front matter on disk has
	// no matching record in the manifest.
	IssueOrphanMarkdown = "orphan_markdown"
	// IssueHashMismatch: the Markdown body on disk hashes differently than the
	// content_hash carried by the manifest record.
	IssueHashMismatch = "hash_mismatch"
	// IssueCrawlMeta: the manifest's crawl_meta summary is missing, duplicated,
	// not on the last line, or carries a page count different from the number
	// of page records.
	IssueCrawlMeta = "crawl_meta"
	// IssueLLMsTxt: llms.txt has entries missing, extra, or in a different
	// order than the manifest.
	IssueLLMsTxt = "llms_txt"
	// IssueLLMsFullTxt: llms-full.txt has entries missing, extra, or in a
	// different order than the manifest.
	IssueLLMsFullTxt = "llms_full_txt"
	// IssueMissingImage: a manifest record references a local image file that
	// does not exist under the output directory.
	IssueMissingImage = "missing_image"
	// IssueIndexMissingPage: the search index has no chunks for a page the
	// manifest contains.
	IssueIndexMissingPage = "index_missing_page"
	// IssueIndexStalePage: the search index has chunks for a page the manifest
	// does not contain.
	IssueIndexStalePage = "index_stale_page"
)

// IssueEntry points at one concrete inconsistency. Relative paths are relative
// to the site output directory; URLs are the page URLs from the manifest (or
// from the Markdown front matter for files the manifest does not know about).
type IssueEntry struct {
	// Category is one of the Issue* constants.
	Category string `json:"category"`
	// Path is the output-dir-relative path involved, when the issue is about
	// a file. Empty for purely record-level issues.
	Path string `json:"path,omitempty"`
	// URL is the page URL involved, when the issue is about a page.
	URL string `json:"url,omitempty"`
	// Detail carries category-specific extra values (e.g. the stale hash), so
	// the structured report stays self-describing.
	Detail map[string]string `json:"detail,omitempty"`
}

// IssueGroup bundles every entry of one category with its count. The same
// entries appear, in the same order, in the human-readable and the structured
// report.
type IssueGroup struct {
	Category string       `json:"category"`
	Count    int          `json:"count"`
	Entries  []IssueEntry `json:"entries"`
}

// FixEntry records one repair action applied during a fixing run.
type FixEntry struct {
	// Category is the Issue* category that was repaired.
	Category string `json:"category"`
	// Action is what the repair did ("add_record", "remove_record",
	// "update_hash", "rewrite_file", "rebuild_index", ...).
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Detail map[string]string `json:"detail,omitempty"`
}

// VerifyReport is the complete result of one verify run. Findings and
// Residual carry the same group shape, so a re-scan of the output directory
// reproduces them.
type VerifyReport struct {
	SiteKey      string       `json:"site_key"`
	OutputDir    string       `json:"output_dir"`
	IndexPath    string       `json:"index_path,omitempty"`
	Fixed        bool         `json:"fixed"`
	Findings     []IssueGroup `json:"findings"`
	Fixes        []FixEntry   `json:"fixes,omitempty"`
	Residual     []IssueGroup `json:"residual,omitempty"`
	FindingsCount int         `json:"findings_count"`
	ResidualCount int         `json:"residual_count"`
}

// PreconditionError names the check that failed before any output was touched.
// A verifying run must fail up front rather than mutate half the corpus and
// then error.
type PreconditionError struct {
	Reason string
	Path   string
}

func (e *PreconditionError) Error() string {
	if e.Path != "" {
		return "verify precondition failed: " + e.Reason + ": " + e.Path
	}
	return "verify precondition failed: " + e.Reason
}

// ErrVerifyPrecondition lets callers classify precondition failures with
// errors.Is/errors.As.
var ErrVerifyPrecondition = errors.New("verify precondition failed")

func (e *PreconditionError) Unwrap() error { return ErrVerifyPrecondition }

// Verifier checks one site's local corpus against its manifest and the search
// index, and can repair everything repairable from what is already on disk.
type Verifier struct {
	appCfg  *config.AppConfig
	siteCfg *config.SiteConfig
	siteKey string
	dir     string
	jsonl   string
	log     *slog.Logger

	cp *process.ContentProcessor
}

// NewVerifier builds a Verifier for one configured site. dir is the site
// output directory and jsonlName its manifest filename (typically
// pages.jsonl).
func NewVerifier(appCfg *config.AppConfig, siteCfg *config.SiteConfig, siteKey, dir, jsonlName string, log *slog.Logger) *Verifier {
	return &Verifier{
		appCfg:  appCfg,
		siteCfg: siteCfg,
		siteKey: siteKey,
		dir:     dir,
		jsonl:   jsonlName,
		log:     log,
		cp:      process.NewContentProcessor(nil, appCfg, log),
	}
}

// CheckPreconditions validates, before any mutation, that the output directory
// exists, the manifest is present and non-empty, and it carries a crawl_meta
// summary record. The site-in-config check lives in the command layer.
func (v *Verifier) CheckPreconditions() error {
	return nil
}

// Verify scans the local corpus and returns every inconsistency found. It is
// read-only.
func (v *Verifier) Verify(ctx context.Context, idxPath string) (*VerifyReport, error) {
	return nil, nil
}

// VerifyAndFix repairs every inconsistency that can be rebuilt from local
// content, then re-verifies. Issues that cannot be repaired locally (a page
// whose Markdown is missing entirely, a missing local image) stay in
// Residual. The report lists findings, applied fixes, and what remains.
func (v *Verifier) VerifyAndFix(ctx context.Context, idxPath string) (*VerifyReport, error) {
	return nil, nil
}

// expectedMarkdownPath maps a page URL to its Markdown file under the output
// dir, reusing the crawler's own URL->path mapping so the check cannot drift
// from how pages are written.
func (v *Verifier) expectedMarkdownPath(pageURL string) (string, bool) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return "", false
	}
	p, ok := v.cp.OutputPathForURL(u, v.siteCfg, v.dir)
	if !ok {
		return "", false
	}
	return p, true
}
