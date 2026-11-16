package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/Sriram-PR/doc-scraper/v2/pkg/config"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/crawler"
	"github.com/Sriram-PR/doc-scraper/v2/pkg/storage/index"
)

// verifyLogger keeps verify runs quiet on stdout: reports go to stdout while
// operational diagnostics land on stderr at warn level and above.
func verifyLogger(stderr io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// Exit codes for the verify subcommand. They are distinct from the crawl
// subcommand's so scripts can tell the three verify outcomes apart:
//
//	0: no inconsistency found
//	4: inconsistencies found, nothing was repaired (read-only run)
//	5: a fixing run finished but unrepairable inconsistencies remain
//
// Precondition and usage failures use the ordinary 1.
const (
	exitVerifyIssues   = 4
	exitVerifyResidual = 5
)

type verifyArgs struct {
	configFile string
	siteKey    string
	fix        bool
	jsonOut    bool
}

// parseVerifyArgs parses the verify subcommand's flags.
func parseVerifyArgs(args []string, out io.Writer) (verifyArgs, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(out)
	var a verifyArgs
	fs.StringVar(&a.configFile, "config", "config.yaml", "Path to config file")
	fs.StringVar(&a.siteKey, "site", "", "Site key from config (required)")
	fs.BoolVar(&a.fix, "fix", false, "Repair every inconsistency rebuildable from local content, then re-verify")
	fs.BoolVar(&a.jsonOut, "json", false, "Emit the report as structured JSON instead of human-readable text")
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage: doc-scraper verify -site <key> [options]\n\n"+
			"Offline consistency check of a site's local output: pages.jsonl, the Markdown tree,\n"+
			"llms.txt/llms-full.txt and the configured search index. No network requests.\n\nOptions:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	if err := extraArgsError(fs); err != nil {
		return a, err
	}
	return a, nil
}

// runVerify is the verify subcommand entry point.
func runVerify(args []string) {
	a, err := parseVerifyArgs(args, os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(doVerify(a, os.Stdout, os.Stderr))
}

// doVerify runs verification (and optionally repair) and returns the process
// exit code. Separated from runVerify so tests can drive it with buffers.
func doVerify(a verifyArgs, stdout, stderr io.Writer) int {
	appCfg, siteCfg, code := loadVerifyConfig(a.configFile, a.siteKey, stderr)
	if code != 0 {
		return code
	}

	dir := appCfg.SiteOutputDir(a.siteKey)
	jsonlName := config.GetEffectiveJSONLOutputFilename(siteCfg, appCfg)
	log := verifyLogger(stderr)
	verifier := crawler.NewVerifier(appCfg, siteCfg, a.siteKey, dir, jsonlName, log)

	if err := verifier.CheckPreconditions(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	idxPath := index.PathAt(appCfg.StateDir)
	ctx := context.Background()

	var report *crawler.VerifyReport
	var err error
	if a.fix {
		report, err = verifier.VerifyAndFix(ctx, idxPath)
	} else {
		report, err = verifier.Verify(ctx, idxPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	if a.jsonOut {
		if err := crawler.RenderVerifyJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	} else {
		crawler.RenderVerifyHuman(stdout, report)
	}

	if a.fix {
		if report.ResidualCount > 0 {
			return exitVerifyResidual
		}
		return 0
	}
	if report.FindingsCount > 0 {
		return exitVerifyIssues
	}
	return 0
}

// loadVerifyConfig loads the config and confirms the site is configured.
func loadVerifyConfig(configPath, siteKey string, stderr io.Writer) (*config.AppConfig, *config.SiteConfig, int) {
	if siteKey == "" {
		fmt.Fprintln(stderr, "Error: -site is required")
		return nil, nil, 1
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return nil, nil, 1
	}
	siteCfg, ok := cfg.Sites[siteKey]
	if !ok {
		fmt.Fprintf(stderr, "Error: site '%s' not found in config\n", siteKey)
		return nil, nil, 1
	}
	return cfg, siteCfg, 0
}
