// Package benchmark evaluates deterministic, original synthetic fixtures only.
package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/embeddings"
	"github.com/AntonioIliev19/conmesh/internal/index"
	"github.com/AntonioIliev19/conmesh/internal/parser"
)

type Options struct {
	Files, Runs, Queries               int
	Profile, Mode, CLI, Commit         string
	Semantic                           *embeddings.Settings
	CLIHash, SourceHash, Compiler, CGO string
}
type GroundTruth struct {
	Query    string   `json:"query"`
	Symbol   string   `json:"symbol"`
	Relevant []string `json:"relevant_paths"`
}
type Sample struct {
	Run            int             `json:"run"`
	Operation      string          `json:"operation"`
	Query          string          `json:"query,omitempty"`
	Millis         float64         `json:"wall_ms"`
	OutputBytes    int             `json:"output_bytes"`
	IndexBytes     int64           `json:"index_bytes,omitempty"`
	Progress       *index.Progress `json:"progress,omitempty"`
	Paths          []string        `json:"paths,omitempty"`
	Precision      float64         `json:"precision_at_5,omitempty"`
	Recall         float64         `json:"recall_at_5,omitempty"`
	ReciprocalRank float64         `json:"reciprocal_rank,omitempty"`
}
type Summary struct {
	Operation string  `json:"operation"`
	Count     int     `json:"count"`
	P50       float64 `json:"p50_ms"`
	P95       float64 `json:"p95_ms"`
	Min       float64 `json:"min_ms"`
	Max       float64 `json:"max_ms"`
	Mean      float64 `json:"mean_ms"`
	StdDev    float64 `json:"stddev_ms"`
}
type Report struct {
	Schema            int               `json:"schema"`
	Started           string            `json:"started_utc"`
	Commit            string            `json:"source_commit"`
	GitHEAD           string            `json:"git_head"`
	GitDirty          bool              `json:"git_dirty"`
	SourceHashAfter   string            `json:"go_source_tree_sha256_after"`
	SourceChanged     bool              `json:"go_source_changed_during_run"`
	CLIChanged        bool              `json:"cli_binary_changed_during_run"`
	CLIHash           string            `json:"cli_binary_sha256"`
	SourceHash        string            `json:"go_source_tree_sha256"`
	Compiler          string            `json:"cc"`
	CGO               string            `json:"cgo_enabled"`
	Go                string            `json:"go_version"`
	OS                string            `json:"os"`
	Arch              string            `json:"arch"`
	LogicalCPUs       int               `json:"logical_cpus"`
	SQLite            string            `json:"sqlite_version"`
	Mode              string            `json:"mode"`
	Profile           string            `json:"profile"`
	Files             int               `json:"files"`
	Runs              int               `json:"runs"`
	SourceBytes       int64             `json:"source_bytes"`
	GroundTruth       []GroundTruth     `json:"ground_truth"`
	Samples           []Sample          `json:"samples"`
	Summaries         []Summary         `json:"summaries"`
	PeakRSSBytes      uint64            `json:"driver_process_peak_rss_bytes"`
	GoHeapSystemBytes uint64            `json:"driver_go_heap_sys_bytes"`
	External          map[string]string `json:"external_gates"`
	Caveats           []string          `json:"caveats"`
}

func Fixture(root string, files int) ([]GroundTruth, int64, error) {
	if files < 4 || files > 20000 {
		return nil, 0, errors.New("fixture files must be 4..20000")
	}
	for _, dir := range []string{"frontend", "backend"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return nil, 0, err
		}
	}
	var total int64
	for i := 0; i < files; i++ {
		name, text := Source(i)
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(text), 0600); err != nil {
			return nil, 0, err
		}
		total += int64(len(text))
	}
	truth := []GroundTruth{}
	for _, i := range []int{0, 1, 2, 3} {
		name, _ := Source(i)
		truth = append(truth, GroundTruth{fmt.Sprintf("Feature%d", i), fmt.Sprintf("Feature%d", i), []string{name}})
	}
	return truth, total, nil
}
func Source(i int) (string, string) {
	if i%2 == 0 {
		return fmt.Sprintf("frontend/feature%05d.ts", i), fmt.Sprintf("// Original CONMESH synthetic benchmark fixture.\nexport class Feature%d {\n  refreshSession(): string { return 'session%d'; }\n  render(): string { return this.refreshSession(); }\n}\n", i, i)
	}
	return fmt.Sprintf("backend/Feature%05d.cs", i), fmt.Sprintf("// Original CONMESH synthetic benchmark fixture.\nnamespace Synthetic;\npublic class Feature%d {\n public string RefreshSession() { return \"session%d\"; }\n public string Endpoint() { return RefreshSession(); }\n}\n", i, i)
}

// Score evaluates unique paths; duplicate chunks cannot inflate retrieval quality.
func Score(paths, relevant []string, k int) (precision, recall, rr float64) {
	truth := map[string]bool{}
	for _, p := range relevant {
		truth[p] = true
	}
	seen := map[string]bool{}
	rank, hits := 0, 0
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		rank++
		if rank > k {
			break
		}
		if truth[p] {
			hits++
			if rr == 0 {
				rr = 1 / float64(rank)
			}
		}
	}
	if k > 0 {
		precision = float64(hits) / float64(k)
	}
	if len(truth) > 0 {
		recall = float64(hits) / float64(len(truth))
	}
	return
}
func summarize(samples []Sample) []Summary {
	groups := map[string][]float64{}
	for _, s := range samples {
		groups[s.Operation] = append(groups[s.Operation], s.Millis)
	}
	names := []string{}
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []Summary{}
	for _, name := range names {
		v := groups[name]
		sort.Float64s(v)
		sum := 0.
		for _, n := range v {
			sum += n
		}
		mean := sum / float64(len(v))
		variance := 0.
		for _, n := range v {
			variance += (n - mean) * (n - mean)
		}
		out = append(out, Summary{name, len(v), v[int(math.Ceil(.5*float64(len(v))))-1], v[int(math.Ceil(.95*float64(len(v))))-1], v[0], v[len(v)-1], mean, math.Sqrt(variance / float64(len(v)))})
	}
	return out
}
func indexSize(root string) int64 {
	var total int64
	filepath.WalkDir(filepath.Join(root, ".conmesh", "state"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func Run(ctx context.Context, o Options) (Report, error) {
	report := Report{Schema: 1, Started: time.Now().UTC().Format(time.RFC3339), Commit: o.Commit, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), Mode: o.Mode, Profile: o.Profile, Files: o.Files, Runs: o.Runs, External: map[string]string{"real_harness_sessions": "unrun: Claude/model sessions unavailable", "llm_tokens_cost": "unrun: no reliable harness telemetry", "laptop_16gb": "unrun: desktop host only", "laptop_32gb": "unrun: desktop host only", "baseline_agent_task_success": "unrun: no controlled harness experiment", "real_model_hybrid": "unrun: no opted-in model configured"}, Caveats: []string{"Synthetic original corpus, not an enterprise repository or a representative quality benchmark.", "Index also contains one allowed .conmesh/config.yaml file; files/source_bytes describe generated TS/C# only.", "Full index uses fresh databases; OS file caches are not flushed. Cold-process query starts a new CLI process, not a cold OS cache.", "Warm queries reuse the benchmark process, but Core opens SQLite per request. Operations run full, unchanged, changed, added, deleted, then queries.", "p50/p95 use nearest-rank empirical percentiles; small repetition counts have limited statistical power.", "Precision uses fixed denominator k=5; ground truth is one exact class path per query. No task-success, token-saving or natural-language generalization claim.", "Peak RSS is the benchmark driver lifetime high-water mark, includes fixture setup and excludes separately launched CLI children; Go heap is reported separately."}}
	report.CLIHash, report.SourceHash, report.Compiler, report.CGO = o.CLIHash, o.SourceHash, o.Compiler, o.CGO
	if !parser.Available() {
		return report, errors.New("native parser required: build benchmark with CGO_ENABLED=1")
	}
	if o.Runs < 1 || o.Runs > 30 || o.Queries < 1 || o.Queries > 100 || o.CLI == "" {
		return report, errors.New("require runs 1..30, queries 1..100 and a native CLI path")
	}
	if o.Mode != "basic" && o.Mode != "hybrid" {
		return report, errors.New("mode must be basic or hybrid")
	}
	if o.Mode == "hybrid" && (o.Semantic == nil || o.Semantic.Validate() != nil) {
		return report, errors.New("hybrid benchmark requires explicitly supplied opted-in real model settings")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return report, err
	}
	err = db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&report.SQLite)
	db.Close()
	if err != nil {
		return report, err
	}
	for run := 1; run <= o.Runs; run++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		root, err := os.MkdirTemp("", "conmesh-benchmark-")
		if err != nil {
			return report, err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return report, err
		}
		err = func() error {
			defer os.RemoveAll(root)
			truth, size, err := Fixture(root, o.Files)
			if err != nil {
				return err
			}
			report.GroundTruth, report.SourceBytes = truth, size
			cfg := config.Default()
			cfg.Indexing.Enabled = true
			cfg.Indexing.Profile = o.Profile
			if o.Mode == "hybrid" {
				cfg.Indexing.Semantic = true
				cfg.Semantic = o.Semantic
			}
			if _, _, err = config.Init(root, cfg); err != nil {
				return err
			}
			core, err := index.New(root)
			if err != nil {
				return err
			}
			measureIndex := func(operation string, expectIndexed, expectDeleted int) error {
				start := time.Now()
				p, err := core.Index(ctx, nil)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(p)
				report.Samples = append(report.Samples, Sample{Run: run, Operation: operation, Millis: float64(time.Since(start).Nanoseconds()) / 1e6, OutputBytes: len(raw), IndexBytes: indexSize(root), Progress: &p})
				if p.Indexed != expectIndexed || p.Deleted != expectDeleted {
					return fmt.Errorf("%s: unexpected incremental counts indexed=%d deleted=%d", operation, p.Indexed, p.Deleted)
				}
				return nil
			}
			if err = measureIndex("index_full", o.Files+1, 0); err != nil {
				return err
			}
			if err = measureIndex("index_unchanged", 0, 0); err != nil {
				return err
			}
			name, text := Source(0)
			if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(text+"// changed fixture\n"), 0600); err != nil {
				return err
			}
			if err = measureIndex("index_changed", 1, 0); err != nil {
				return err
			}
			name, text = Source(o.Files)
			if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(text), 0600); err != nil {
				return err
			}
			if err = measureIndex("index_added", 1, 0); err != nil {
				return err
			}
			if err = os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				return err
			}
			if err = measureIndex("index_deleted", 0, 1); err != nil {
				return err
			}
			if o.Mode == "hybrid" {
				start := time.Now()
				p, err := core.IndexSemantic(ctx, "")
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(p)
				report.Samples = append(report.Samples, Sample{Run: run, Operation: "semantic_index", Millis: float64(time.Since(start).Nanoseconds()) / 1e6, OutputBytes: len(raw)})
				report.External["real_model_hybrid"] = "measured: explicitly configured local model"
			}
			for iteration := 0; iteration < o.Queries; iteration++ {
				start := time.Now()
				versionCmd := exec.CommandContext(ctx, o.CLI, "--version")
				versionRaw, versionErr := versionCmd.Output()
				if versionErr != nil {
					return errors.New("CLI startup failed")
				}
				report.Samples = append(report.Samples, Sample{Run: run, Operation: "cold_process_version", Millis: float64(time.Since(start).Nanoseconds()) / 1e6, OutputBytes: len(versionRaw)})
				for _, truth := range truth {
					for _, kind := range []string{"fts", "symbol"} {
						q := index.Query{Text: truth.Query, Limit: 5}
						start := time.Now()
						var evidence []index.Evidence
						var err error
						if kind == "symbol" {
							evidence, err = core.FindSymbol(ctx, q)
						} else if o.Mode == "hybrid" {
							var result index.HybridResult
							result, err = core.Hybrid(ctx, q)
							evidence = result.Evidence
						} else {
							evidence, err = core.Search(ctx, q)
						}
						if err != nil {
							return err
						}
						raw, _ := json.Marshal(evidence)
						paths := []string{}
						for _, e := range evidence {
							paths = append(paths, e.Path)
						}
						p, r, rr := Score(paths, truth.Relevant, 5)
						operation := "warm_" + kind
						if kind == "fts" && o.Mode == "hybrid" {
							operation = "warm_hybrid"
						}
						report.Samples = append(report.Samples, Sample{Run: run, Operation: operation, Query: truth.Query, Millis: float64(time.Since(start).Nanoseconds()) / 1e6, OutputBytes: len(raw), Paths: paths, Precision: p, Recall: r, ReciprocalRank: rr})
					}
				}
				// Each cold-process sample launches the independently built production CLI.
				truth := truth[iteration%len(truth)]
				for _, command := range []string{"search", "symbol"} {
					args := []string{command, truth.Query, "--limit", "5"}
					if command == "search" && o.Mode == "hybrid" {
						args = append(args, "--semantic")
					}
					start := time.Now()
					cmd := exec.CommandContext(ctx, o.CLI, args...)
					cmd.Dir = root
					raw, err := cmd.Output()
					if err != nil {
						return errors.New("cold-process CLI query failed")
					}
					paths := []string{}
					if command == "search" && o.Mode == "hybrid" {
						var result index.HybridResult
						if json.Unmarshal(raw, &result) != nil {
							return errors.New("invalid CLI result")
						}
						for _, e := range result.Evidence {
							paths = append(paths, e.Path)
						}
					} else {
						var result []index.Evidence
						if json.Unmarshal(raw, &result) != nil {
							return errors.New("invalid CLI result")
						}
						for _, e := range result {
							paths = append(paths, e.Path)
						}
					}
					p, r, rr := Score(paths, truth.Relevant, 5)
					report.Samples = append(report.Samples, Sample{Run: run, Operation: "cold_process_" + command, Query: truth.Query, Millis: float64(time.Since(start).Nanoseconds()) / 1e6, OutputBytes: len(raw), Paths: paths, Precision: p, Recall: r, ReciprocalRank: rr})
				}
			}
			return nil
		}()
		if err != nil {
			return report, err
		}
	}
	report.Summaries = summarize(report.Samples)
	report.PeakRSSBytes = peakRSS()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	report.GoHeapSystemBytes = mem.HeapSys
	return report, nil
}
