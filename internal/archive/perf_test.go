package archive

import (
	"context"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// perfArchive builds a ~20 MB zip of 500 incompressible files.
func perfArchive(tb testing.TB) string {
	tb.Helper()
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic test data.
	ents := []zent{{name: "index.html", body: page}}
	for i := range 499 {
		buf := make([]byte, 40<<10)
		for j := range buf {
			buf[j] = byte(rng.UintN(256))
		}
		ents = append(ents, zent{name: "assets/" + strconv.Itoa(i%10) + "/f" + strconv.Itoa(i) + ".bin", body: string(buf)})
	}
	return writeFile(tb, zipBytes(tb, ents))
}

func TestExtractPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	src := perfArchive(t)
	_, dest := sandbox(t)
	start := time.Now()
	res, err := Extract(context.Background(), src, dest, bigLimits, testNow)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 500 || res.Bytes < 19<<20 {
		t.Fatalf("unexpected result %+v", res)
	}
	t.Logf("20 MB / 500 files extracted in %v", elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("extraction took %v, budget is 5s", elapsed)
	}
}

func BenchmarkExtract20MB(b *testing.B) {
	src := perfArchive(b)
	base := b.TempDir()
	for i := 0; b.Loop(); i++ {
		dest := filepath.Join(base, strconv.Itoa(i))
		if _, err := Extract(context.Background(), src, dest, bigLimits, testNow); err != nil {
			b.Fatal(err)
		}
	}
}
