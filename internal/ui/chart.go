package ui

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Geometry of the daily views chart in SVG user units. The SVG scales with
// its container; on narrow screens the wrapper scrolls instead of shrinking
// the text below legibility (see .chart in app.css).
const (
	chartW       = 640.0
	chartH       = 220.0
	chartLeft    = 44.0 // y tick labels
	chartRight   = 18.0
	chartTop     = 22.0 // label of the highest bar
	chartBottom  = 30.0 // date labels
	chartMaxBar  = 24.0
	chartGap     = 2.0
	chartRadius  = 4.0
	chartLabelOn = 7 // a date label every week, counted back from today
)

// niceCeil rounds v up to 1, 2 or 5 times a power of ten (at least 2, so
// the middle tick is a whole number).
func niceCeil(v int64) int64 {
	if v <= 2 {
		return 2
	}
	p := int64(math.Pow(10, math.Floor(math.Log10(float64(v)))))
	for _, m := range []int64{1, 2, 5, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return 10 * p
}

func f1(v float64) string { return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) }

// barPath draws a column with a rounded top and a square base.
func barPath(x, y, w, h float64) string {
	r := math.Min(chartRadius, math.Min(w/2, h))
	base := y + h
	return "M" + f1(x) + " " + f1(base) + "V" + f1(y+r) + "Q" + f1(x) + " " + f1(y) + " " + f1(x+r) + " " + f1(y) +
		"H" + f1(x+w-r) + "Q" + f1(x+w) + " " + f1(y) + " " + f1(x+w) + " " + f1(y+r) + "V" + f1(base) + "Z"
}

// viewsChart renders the daily views as an accessible SVG column chart. Every
// text comes from numbers, dates and the translation catalog, and is escaped.
func viewsChart(l *locale, days []domain.DayStat, idPrefix string) template.HTML {
	if len(days) == 0 {
		return ""
	}
	var peak int64
	peakAt := 0
	for i, d := range days {
		if d.Views > peak {
			peak, peakAt = d.Views, i
		}
	}
	top := niceCeil(peak)
	plotW := chartW - chartLeft - chartRight
	plotH := chartH - chartTop - chartBottom
	base := chartTop + plotH
	slot := plotW / float64(len(days))
	barW := math.Min(chartMaxBar, slot-chartGap)
	y := func(v int64) float64 { return base - float64(v)/float64(top)*plotH }

	var b strings.Builder
	esc := template.HTMLEscapeString
	titleID, descID := idPrefix+"-title", idPrefix+"-desc"
	fmt.Fprintf(&b, `<svg class="chart__svg" viewBox="0 0 %s %s" role="img" aria-labelledby="%s %s">`,
		f1(chartW), f1(chartH), titleID, descID)
	fmt.Fprintf(&b, `<title id="%s">%s</title>`, titleID, esc(l.T("stats.chart.title", "days", len(days))))
	fmt.Fprintf(&b, `<desc id="%s">%s</desc>`, descID, esc(l.T("stats.chart.desc",
		"from", l.ShortDay(days[0].Day), "to", l.ShortDay(days[len(days)-1].Day), "max", l.Num(peak))))

	b.WriteString(`<g class="chart__grid" aria-hidden="true">`)
	for _, v := range []int64{0, top / 2, top} {
		cls := "chart__line"
		if v == 0 {
			cls = "chart__base"
		}
		fmt.Fprintf(&b, `<line class="%s" x1="%s" x2="%s" y1="%s" y2="%s"/>`, cls, f1(chartLeft), f1(chartW-chartRight),
			f1(y(v)), f1(y(v)))
		fmt.Fprintf(&b, `<text class="chart__tick" x="%s" y="%s">%s</text>`, f1(chartLeft-8), f1(y(v)+4), esc(l.Num(v)))
	}
	b.WriteString(`</g>`)

	b.WriteString(`<g class="chart__days">`)
	for i, d := range days {
		x := chartLeft + float64(i)*slot + (slot-barW)/2
		fmt.Fprintf(&b, `<g class="chart__day"><title>%s</title>`, esc(l.T("stats.chart.bar",
			"date", l.ShortDay(d.Day), "views", l.Num(d.Views), "visitors", l.Num(d.Visitors))))
		fmt.Fprintf(&b, `<rect class="chart__hit" x="%s" y="%s" width="%s" height="%s"/>`,
			f1(chartLeft+float64(i)*slot), f1(chartTop), f1(slot), f1(plotH))
		if d.Views > 0 {
			fmt.Fprintf(&b, `<path class="chart__bar" d="%s"/>`, barPath(x, y(d.Views), barW, base-y(d.Views)))
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g>`)

	b.WriteString(`<g aria-hidden="true">`)
	if peak > 0 {
		fmt.Fprintf(&b, `<text class="chart__value" x="%s" y="%s">%s</text>`,
			f1(chartLeft+(float64(peakAt)+0.5)*slot), f1(y(peak)-6), esc(l.Num(peak)))
	}
	for i := len(days) - 1; i >= 0; i -= chartLabelOn {
		fmt.Fprintf(&b, `<text class="chart__date" x="%s" y="%s">%s</text>`,
			f1(chartLeft+(float64(i)+0.5)*slot), f1(chartH-8), esc(l.ShortDay(days[i].Day)))
	}
	b.WriteString(`</g></svg>`)
	return template.HTML(b.String()) //nolint:gosec // built from numbers and escaped catalog text only
}

// ShortDay formats a YYYY-MM-DD day as "12 Nov" / "12 ноя".
func (l *locale) ShortDay(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return strconv.Itoa(t.Day()) + " " + l.T("month."+strconv.Itoa(int(t.Month())))
}

// Num formats an integer with thousands separators: "12 345" / "12,345".
func (l *locale) Num(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	sep := ","
	if l.Lang == "ru" {
		sep = " "
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
