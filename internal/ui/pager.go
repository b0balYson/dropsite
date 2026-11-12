package ui

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/b0balYson/dropsite/internal/service"
)

// adminPageSize is the number of rows on admin list pages.
const adminPageSize = 50

// pager links neighbouring pages of a list, keeping the current filters.
type pager struct {
	Page  int
	Pages int
	Total int
	Prev  string
	Next  string
}

// pageParam reads the 1-based page number from the query string.
func pageParam(r *http.Request) (int, service.Page) {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		n = 1
	}
	return n, service.Page{Limit: adminPageSize, Offset: (n - 1) * adminPageSize}
}

func newPager(base string, q url.Values, page, total int) pager {
	pages := max(1, (total+adminPageSize-1)/adminPageSize)
	p := pager{Page: page, Pages: pages, Total: total}
	link := func(n int) string {
		v := url.Values{}
		for k, vals := range q {
			if k != "page" && len(vals) > 0 && vals[0] != "" {
				v.Set(k, vals[0])
			}
		}
		if n > 1 {
			v.Set("page", strconv.Itoa(n))
		}
		if enc := v.Encode(); enc != "" {
			return base + "?" + enc
		}
		return base
	}
	if page > 1 {
		p.Prev = link(page - 1)
	}
	if page < pages {
		p.Next = link(page + 1)
	}
	return p
}
