package quality

import (
	"fmt"
	"strings"
)

type penalty struct {
	n, each, limit int
}

// score applies capped penalties per category.
func score(c Counts) int {
	s := 100
	for _, p := range []penalty{
		{c.BrokenLinks, 5, 25},
		{c.MissingAssets, 5, 25},
		{c.ExternalHosts, 5, 20},
		{c.MissingTitle, 5, 10},
		{c.MissingViewport, 5, 10},
		{c.MissingDescription, 2, 6},
		{c.MissingLang, 2, 6},
		{c.ImagesWithoutAlt, 1, 10},
		{c.HeavyPages, 5, 10},
	} {
		s -= min(p.n*p.each, p.limit)
	}
	return max(s, 0)
}

func grade(s int) string {
	switch {
	case s >= 90:
		return "A"
	case s >= 75:
		return "B"
	case s >= 50:
		return "C"
	}
	return "D"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// issues lists the main problems, most important first, one line per
// category with an example.
func issues(r *Report) []string {
	var out []string
	add := func(n int, one, many, example string) {
		if n == 0 || len(out) >= maxIssues {
			return
		}
		line := plural(n, one, many)
		if example != "" {
			line += ", e.g. " + example
		}
		out = append(out, line)
	}
	first := func(l []Issue, f func(Issue) string) string {
		if len(l) == 0 {
			return ""
		}
		return f(l[0])
	}
	firstPage := func(l []string) string {
		if len(l) == 0 {
			return ""
		}
		return l[0]
	}
	c := r.Counts
	add(c.MissingAssets, "missing file", "missing files", first(r.MissingAssets, func(i Issue) string {
		return i.Target + " used in " + i.Page
	}))
	add(c.BrokenLinks, "broken link", "broken links", first(r.BrokenLinks, func(i Issue) string {
		return i.Target + " on " + i.Page
	}))
	add(c.ExternalResources, "file loaded from another server (ship it with the site)",
		"files loaded from other servers (ship them with the site)", first(r.ExternalResources, func(i Issue) string {
			return i.Kind + " " + i.Target + " in " + i.Page
		}))
	add(c.MissingViewport, "page without a viewport meta tag (breaks on phones)",
		"pages without a viewport meta tag (break on phones)", firstPage(r.PagesMissing.Viewport))
	add(c.MissingTitle, "page without a <title>", "pages without a <title>", firstPage(r.PagesMissing.Title))
	heavy := ""
	if c.HeavyPages > 0 && len(r.HeaviestPages) > 0 {
		heavy = fmt.Sprintf("%s (%.1f MB)", r.HeaviestPages[0].Page, float64(r.HeaviestPages[0].Bytes)/(1<<20))
	}
	add(c.HeavyPages, "page heavier than 3 MB with its files", "pages heavier than 3 MB with their files", heavy)
	add(c.ImagesWithoutAlt, "image without alt text", "images without alt text", first(r.ImagesWithoutAlt, func(i Issue) string {
		return i.Target + " on " + i.Page
	}))
	add(c.MissingDescription, "page without a meta description", "pages without a meta description",
		firstPage(r.PagesMissing.Description))
	add(c.MissingLang, "page without a lang attribute on <html>", "pages without a lang attribute on <html>",
		firstPage(r.PagesMissing.Lang))
	if out == nil {
		out = []string{}
	}
	return out
}

// finish computes the score, grade, summary sentence and normal form.
func (r *Report) finish() {
	r.Counts.ExternalHosts = len(r.ExternalHosts)
	r.Score = score(r.Counts)
	r.Grade = grade(r.Score)
	main := issues(r)
	if len(main) == 0 {
		r.Summary = fmt.Sprintf("Quality %d/100 (%s): no problems found.", r.Score, r.Grade)
	} else {
		r.Summary = fmt.Sprintf("Quality %d/100 (%s): %s.", r.Score, r.Grade, strings.Join(main, "; "))
	}
	if len(r.ExternalHosts) > MaxListItems {
		r.ExternalHosts = r.ExternalHosts[:MaxListItems]
	}
	r.Normalize()
}

// Summarize returns the short form of a report for publication results.
func Summarize(r *Report) *Summary {
	if r == nil {
		return nil
	}
	return &Summary{Score: r.Score, Grade: r.Grade, Summary: r.Summary, Issues: issues(r)}
}
