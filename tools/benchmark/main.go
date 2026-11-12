// Command benchmark generates only temporary synthetic repositories.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/AntonioIliev19/aikit-public/internal/benchmark"
	"github.com/AntonioIliev19/aikit-public/internal/config"
	"github.com/AntonioIliev19/aikit-public/internal/embeddings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchmark:", err)
		os.Exit(1)
	}
}
func sourceHash() (string, error) {
	paths := []string{"go.mod", "go.sum"}
	for _, dir := range []string{"cmd", "internal", "tools"} {
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			return "", fmt.Errorf("cannot hash Go source tree")
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s:%d:", filepath.ToSlash(path), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func run() error {
	var o benchmark.Options
	var out, settings string
	flag.IntVar(&o.Files, "files", 1000, "original synthetic TS/C# source files (4..20000)")
	flag.IntVar(&o.Runs, "runs", 5, "fresh fixture repetitions")
	flag.IntVar(&o.Queries, "queries", 5, "query rounds per repetition")
	flag.StringVar(&o.Profile, "profile", "balanced", "eco, balanced, performance")
	flag.StringVar(&o.Mode, "mode", "basic", "basic or hybrid")
	flag.StringVar(&o.CLI, "cli", "", "absolute path to independently built native aikit executable")
	flag.StringVar(&o.Commit, "commit", "unknown", "source commit plus dirty status if applicable")
	flag.StringVar(&out, "out", "", "new output JSON path; CSV sibling is also created")
	flag.StringVar(&settings, "semantic-settings", "", "explicit local real-model Settings JSON for hybrid only; never downloaded")
	flag.Parse()
	if out == "" || flag.NArg() != 0 {
		return fmt.Errorf("require --out and no positional arguments")
	}
	if !filepath.IsAbs(o.CLI) {
		return fmt.Errorf("--cli must be absolute")
	}
	cliData, err := os.ReadFile(o.CLI)
	if err != nil {
		return fmt.Errorf("cannot read benchmark CLI")
	}
	sum := sha256.Sum256(cliData)
	o.CLIHash = hex.EncodeToString(sum[:])
	o.SourceHash, err = sourceHash()
	if err != nil {
		return err
	}
	gitHead, gitErr := exec.Command("git", "rev-parse", "HEAD").Output()
	gitStatus, statusErr := exec.Command("git", "status", "--porcelain").Output()
	for _, item := range []struct {
		name   string
		target *string
	}{{"CC", &o.Compiler}, {"CGO_ENABLED", &o.CGO}} {
		raw, err := exec.Command("go", "env", item.name).Output()
		if err != nil {
			return fmt.Errorf("cannot record Go toolchain settings")
		}
		*item.target = strings.TrimSpace(string(raw))
	}
	if settings != "" {
		if o.Mode != "hybrid" {
			return fmt.Errorf("semantic settings require hybrid mode")
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			return fmt.Errorf("cannot read explicitly supplied semantic settings")
		}
		var s embeddings.Settings
		if config.Decode(data, &s) != nil || s.Validate() != nil {
			return fmt.Errorf("invalid opted-in semantic settings")
		}
		o.Semantic = &s
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	report, err := benchmark.Run(ctx, o)
	if err != nil {
		return err
	}
	if gitErr == nil {
		report.GitHEAD = strings.TrimSpace(string(gitHead))
	} else {
		report.GitHEAD = "unavailable"
	}
	report.GitDirty = statusErr != nil || len(gitStatus) > 0
	report.SourceHashAfter, err = sourceHash()
	if err != nil {
		return err
	}
	report.SourceChanged = report.SourceHashAfter != o.SourceHash
	cliAfter, err := os.ReadFile(o.CLI)
	if err != nil {
		return err
	}
	afterSum := sha256.Sum256(cliAfter)
	report.CLIChanged = hex.EncodeToString(afterSum[:]) != o.CLIHash
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	csvFile, err := os.OpenFile(out+".csv", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	w := csv.NewWriter(csvFile)
	w.Write([]string{"run", "operation", "query", "wall_ms", "output_bytes", "index_bytes", "precision_at_5", "recall_at_5", "reciprocal_rank"})
	for _, s := range report.Samples {
		w.Write([]string{strconv.Itoa(s.Run), s.Operation, s.Query, strconv.FormatFloat(s.Millis, 'f', 6, 64), strconv.Itoa(s.OutputBytes), strconv.FormatInt(s.IndexBytes, 10), strconv.FormatFloat(s.Precision, 'f', 6, 64), strconv.FormatFloat(s.Recall, 'f', 6, 64), strconv.FormatFloat(s.ReciprocalRank, 'f', 6, 64)})
	}
	w.Flush()
	err = w.Error()
	closeErr = csvFile.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("Recorded %d samples; SQLite %s; driver peak RSS %d bytes\n", len(report.Samples), report.SQLite, report.PeakRSSBytes)
	return nil
}
