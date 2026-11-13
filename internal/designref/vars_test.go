package designref

import (
	"strings"
	"testing"
	"time"
)

// A chain of custom properties that each reference the next one many times
// must not expand exponentially (memory and CPU exhaustion).
func TestVarExpansionIsBounded(t *testing.T) {
	const fanout = 400
	var b strings.Builder
	b.WriteString(":root{")
	names := []string{"--a", "--b", "--c", "--d", "--e"}
	for i := 0; i+1 < len(names); i++ {
		b.WriteString(names[i] + ":" + strings.Repeat("var("+names[i+1]+") ", fanout) + ";")
	}
	b.WriteString("--e:#d9480f;}")
	for i := range 200 {
		b.WriteString(".x" + strings.Repeat("y", i%7) + "{color:var(--a)}")
	}
	page, err := ParsePage("https://example.com/", []byte("<html><body></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	st := newStyleStats()
	st.vars["--a"] = strings.Repeat("var(--b) ", fanout)
	if got := st.resolve("var(--a)"); len(got) > maxResolved {
		t.Fatalf("resolved value is %d bytes, want at most %d", len(got), maxResolved)
	}
	start := time.Now()
	_ = page.Brief([]string{b.String()}, Stylesheets{Fetched: 1})
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("brief took %s", d)
	}
}
