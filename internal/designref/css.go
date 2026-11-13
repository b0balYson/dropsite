package designref

import (
	"net/url"
	"strings"
)

// Bounds of the CSS scanner.
const (
	maxRules     = 20000
	maxNestDepth = 8
)

// decl is one CSS declaration.
type decl struct {
	prop, value string
}

// rule is a style rule with its declarations.
type rule struct {
	sel   string
	decls []decl
	dark  bool // inside @media (prefers-color-scheme: dark)
}

// sheet is what the scanner found in CSS text.
type sheet struct {
	rules     []rule
	fontFaces []string
	imports   []string
	darkMedia bool
	truncated bool
}

// parseCSS scans CSS text into rules. It is a tolerant scanner, not a full
// parser: comments are dropped, @media, @supports, @layer and @container are
// unwrapped, @keyframes and unknown at-rules are skipped, and nested rules
// get their parent selector prepended.
func parseCSS(text string) *sheet {
	s := &sheet{}
	s.body("", stripComments(text), false, 0)
	return s
}

func stripComments(t string) string {
	var b strings.Builder
	for {
		i := strings.Index(t, "/*")
		if i < 0 {
			b.WriteString(t)
			return b.String()
		}
		b.WriteString(t[:i])
		j := strings.Index(t[i+2:], "*/")
		if j < 0 {
			return b.String()
		}
		t = t[i+2+j+2:]
	}
}

// scan returns the index of the first of the stop bytes outside strings and
// parentheses, or -1.
func scan(t string, from int, stops string) int {
	var quote byte
	paren := 0
	for i := from; i < len(t); i++ {
		c := t[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			paren++
		case c == ')':
			if paren > 0 {
				paren--
			}
		case paren == 0 && strings.IndexByte(stops, c) >= 0:
			return i
		}
	}
	return -1
}

// matching returns the index of the brace closing the one at open, or -1.
func matching(t string, open int) int {
	depth := 0
	for i := open; i < len(t); {
		j := scan(t, i, "{}")
		if j < 0 {
			return -1
		}
		if t[j] == '{' {
			depth++
		} else {
			depth--
			if depth == 0 {
				return j
			}
		}
		i = j + 1
	}
	return -1
}

// body scans a block: declarations belong to sel, nested blocks are rules
// or at-rules.
func (s *sheet) body(sel, t string, dark bool, depth int) {
	var decls []decl
	for i := 0; i < len(t); {
		if len(s.rules) >= maxRules {
			s.truncated = true
			return
		}
		j := scan(t, i, ";{}")
		if j < 0 {
			decls = appendDecl(decls, t[i:])
			break
		}
		switch t[j] {
		case ';', '}':
			s.statement(strings.TrimSpace(t[i:j]), &decls)
			i = j + 1
		case '{':
			end := matching(t, j)
			if end < 0 {
				s.truncated = true
				end = len(t)
			}
			prelude := strings.TrimSpace(t[i:j])
			inner := t[j+1 : end]
			if depth < maxNestDepth {
				s.block(sel, prelude, inner, dark, depth)
			} else {
				s.truncated = true
			}
			i = end + 1
		}
	}
	if sel != "" && len(decls) > 0 {
		s.rules = append(s.rules, rule{sel: sel, decls: decls, dark: dark})
	}
}

// statement handles one declaration or a block-less at-rule.
func (s *sheet) statement(stmt string, decls *[]decl) {
	if strings.HasPrefix(stmt, "@") {
		if strings.HasPrefix(strings.ToLower(stmt), "@import") {
			if u := importURL(stmt[len("@import"):]); u != "" {
				s.imports = append(s.imports, u)
			}
		}
		return
	}
	*decls = appendDecl(*decls, stmt)
}

func (s *sheet) block(parent, prelude, inner string, dark bool, depth int) {
	if !strings.HasPrefix(prelude, "@") {
		sel := prelude
		if parent != "" {
			sel = parent + " " + strings.ReplaceAll(prelude, "&", "")
		}
		s.body(sel, inner, dark, depth+1)
		return
	}
	name := strings.ToLower(prelude)
	if k := strings.IndexAny(name, " \t\n("); k > 0 {
		name = name[:k]
	}
	switch name {
	case "@media", "@supports", "@layer", "@container", "@document", "@scope":
		isDark := name == "@media" && darkQuery(prelude)
		if isDark {
			s.darkMedia = true
		}
		s.body(parent, inner, dark || isDark, depth+1)
	case "@font-face":
		for _, d := range appendDecl(nil, inner) {
			if d.prop == "font-family" {
				if f := families(d.value); len(f) > 0 {
					s.fontFaces = append(s.fontFaces, f[0])
				}
			}
		}
	}
}

func darkQuery(prelude string) bool {
	p := strings.ReplaceAll(strings.ToLower(prelude), " ", "")
	return strings.Contains(p, "prefers-color-scheme:dark")
}

// appendDecl parses "prop: value" (several separated by semicolons).
func appendDecl(out []decl, t string) []decl {
	for _, part := range splitTop(t, ';') {
		k := strings.IndexByte(part, ':')
		if k <= 0 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(part[:k]))
		val := strings.TrimSpace(part[k+1:])
		val = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(val, "!important")), "!important"))
		if prop == "" || val == "" || strings.ContainsAny(prop, " {}") {
			continue
		}
		out = append(out, decl{prop: prop, value: val})
	}
	return out
}

// splitTop splits t at sep outside strings and parentheses.
func splitTop(t string, sep byte) []string {
	var out []string
	for i := 0; ; {
		j := scan(t, i, string(sep))
		if j < 0 {
			if s := strings.TrimSpace(t[i:]); s != "" {
				out = append(out, s)
			}
			return out
		}
		if s := strings.TrimSpace(t[i:j]); s != "" {
			out = append(out, s)
		}
		i = j + 1
	}
}

// importURL reads the address of an @import statement.
func importURL(rest string) string {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(strings.ToLower(rest), "url(") {
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return ""
		}
		rest = rest[4:end]
	} else if k := strings.IndexAny(rest, " \t"); k > 0 {
		rest = rest[:k]
	}
	return strings.Trim(strings.TrimSpace(rest), `"'`)
}

// families splits a font-family value into names without quotes.
func families(v string) []string {
	var out []string
	for _, f := range splitTop(v, ',') {
		f = strings.TrimSpace(strings.Trim(strings.TrimSpace(f), `"'`))
		if f != "" && !strings.HasPrefix(f, "var(") {
			out = append(out, f)
		}
	}
	return out
}

// Imports returns the absolute addresses of the @import rules of css,
// resolved against base.
func Imports(css, base string) []string {
	b, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, raw := range parseCSS(css).imports {
		u, err := b.Parse(raw)
		if err == nil {
			out = append(out, u.String())
		}
	}
	return out
}
