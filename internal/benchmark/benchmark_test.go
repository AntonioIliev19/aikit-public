package benchmark

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFixtureAndGroundTruth(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	ga, sa, err := Fixture(a, 20)
	if err != nil {
		t.Fatal(err)
	}
	gb, sb, err := Fixture(b, 20)
	if err != nil {
		t.Fatal(err)
	}
	if sa != sb || !reflect.DeepEqual(ga, gb) {
		t.Fatal("fixture not deterministic")
	}
	for i := 0; i < 20; i++ {
		name, want := Source(i)
		got, err := os.ReadFile(filepath.Join(a, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatal("fixture differs")
		}
	}
	if _, _, err := Fixture(a, 0); err == nil {
		t.Fatal("unbounded fixture accepted")
	}
}
func TestMetricsDeduplicateAndPercentiles(t *testing.T) {
	p, r, rr := Score([]string{"wrong", "right", "right"}, []string{"right"}, 5)
	if p != .2 || r != 1 || rr != .5 {
		t.Fatal("incorrect quality metrics")
	}
	s := summarize([]Sample{{Operation: "x", Millis: 3}, {Operation: "x", Millis: 1}, {Operation: "x", Millis: 2}})
	if len(s) != 1 || s[0].P50 != 2 || s[0].P95 != 3 || s[0].Mean != 2 {
		t.Fatal("incorrect summary")
	}
}
