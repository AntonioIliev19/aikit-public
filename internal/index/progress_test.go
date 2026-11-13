package index

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProgressAllOutcomes(t *testing.T) {
	native(t)
	for _, tc := range []struct {
		name                          string
		files                         map[string]string
		processed, indexed, malformed int
	}{
		{"empty", nil, 0, 0, 0},
		{"scan-skipped", map[string]string{".env": "fixture"}, 0, 0, 0},
		// Malformed sources are indexed as plain text, so they count as indexed.
		{"malformed-and-binary", map[string]string{"bad.ts": "export function Broken( {", "binary.txt": "\x00"}, 2, 1, 1},
		{"mixed", map[string]string{"good.ts": "export const Good = 1;", "bad.ts": "export function Broken( {", "binary.txt": "\x00"}, 3, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, dir := fixture(t, true, tc.files)
			write(t, dir, ".conmeshignore", ".conmesh/\n.conmeshignore\n")
			for run := 0; run < 2; run++ {
				var events []Progress
				p, err := c.Index(context.Background(), func(p Progress) { events = append(events, p) })
				if err != nil {
					t.Fatal(err)
				}
				if len(events) < 4 || events[0].Phase != "scanning" || events[len(events)-2].Phase != "finalizing" || events[len(events)-1].Phase != "done" {
					t.Fatal("missing lifecycle events", events)
				}
				processed := 0
				for _, e := range events {
					if e.Processed < processed || e.Processed > e.Scanned {
						t.Fatal("invalid processed counter", e)
					}
					processed = e.Processed
				}
				// Malformed files are parsed (and counted) only when their bytes change.
				malformed := tc.malformed
				if run == 1 {
					malformed = 0
				}
				if p.Processed != tc.processed || p.Malformed != malformed || p.Processed != p.Scanned || p.SkipReasons.Total() != p.Skipped {
					t.Fatal("missing skipped completion", p)
				}
				if run == 0 && p.Indexed != tc.indexed || run == 1 && p.Unchanged != tc.indexed {
					t.Fatal("wrong work outcomes", p)
				}
				data, err := json.Marshal(p)
				if err != nil || strings.Contains(string(data), "Phase") || strings.Contains(string(data), "Processed") || strings.Contains(string(data), "phase") || strings.Contains(string(data), "processed") {
					t.Fatal("transient progress leaked into JSON")
				}
			}
		})
	}
}

func TestProgressFailureDuringFinalization(t *testing.T) {
	native(t)
	c, dir := fixture(t, true, map[string]string{"a.ts": "export const A = 1;"})
	write(t, dir, ".conmeshignore", ".conmesh/\n.conmeshignore\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var last Progress
	_, err := c.Index(ctx, func(p Progress) {
		last = p
		if p.Phase == "done" {
			t.Error("cancelled transaction reported complete")
		}
		if p.Phase == "finalizing" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || last.Phase != "failed" || last.Processed != 1 {
		t.Fatal("missing failure progress", last, err)
	}
	status, err := c.Status(context.Background())
	if err != nil || status.IndexedAt != "" {
		t.Fatal("cancelled run committed", err)
	}
}
