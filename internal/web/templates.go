package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/meta"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// pages maps a page name to its parsed template set (layout + page).
var pages = map[string]*template.Template{}

func init() {
	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		panic(err)
	}
	for _, n := range names {
		base := strings.TrimSuffix(path.Base(n), ".html")
		if base == "layout" || strings.HasPrefix(base, "_") {
			continue
		}
		pages[base] = template.Must(template.New(base).Funcs(funcs).ParseFS(templateFS,
			"templates/layout.html", "templates/_*.html", n))
	}
}

var funcs = template.FuncMap{
	"bytes":    humanBytes,
	"n":        humanInt,
	"date":     func(t time.Time) string { return nz(t, "2006-01-02") },
	"datetime": func(t time.Time) string { return nz(t, "2006-01-02 15:04") },
	"dur":      humanDur,
	"str": func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	},
	"int": func(p *int64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatInt(*p, 10)
	},
	"flt": func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', -1, 64)
	},
	"fps": func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', 3, 64)
	},
	"res": func(m meta.Fields) string {
		if m.Width == nil {
			return ""
		}
		h := int64(0)
		if m.Height != nil {
			h = *m.Height
		}
		return fmt.Sprintf("%dx%d", *m.Width, h)
	},
	"durp": func(p *float64) string {
		if p == nil {
			return ""
		}
		return humanDur(*p)
	},
	"tm": func(p *time.Time) string {
		if p == nil {
			return ""
		}
		return p.Format("2006-01-02 15:04")
	},
	"bool": func(p *bool) string {
		if p == nil {
			return ""
		}
		if *p {
			return "Yes"
		}
		return "No"
	},
	"crumbs":   crumbs,
	"parent":   parentOf,
	"basename": path.Base,
	"add":      func(a, b int) int { return a + b },
	"sub":      func(a, b int) int { return a - b },
	"used":     func(capacity, free int64) int64 { return capacity - free },
	"pct": func(part, total int64) int {
		if total <= 0 {
			return 0
		}
		return int(part * 100 / total)
	},
	"sortlink": sortLink,
	"kindBadge": func(kind string) string {
		switch kind {
		case "r3d", "arriraw", "sony", "braw":
			return "badge-primary"
		case "video":
			return "badge-primary"
		case "audio":
			return "badge-accent"
		case "sidecar":
			return "badge-neutral"
		case "dir":
			return "badge-ghost"
		}
		return "badge-neutral"
	},
	"json":  func(v any) string { return fmt.Sprintf("%s", v) },
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	},
	"source": func(sources map[string]string, field string) string { return sources[field] },
	"q":      url.QueryEscape,
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[fmt.Sprint(kv[i])] = kv[i+1]
		}
		return m
	},
	"int64ish": func(n int64) int64 { return n },
}

func nz(t time.Time, layout string) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(layout)
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

func humanInt(n any) string {
	var v int64
	switch x := n.(type) {
	case int:
		v = int64(x)
	case int64:
		v = x
	default:
		return fmt.Sprint(n)
	}
	s := strconv.FormatInt(v, 10)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func humanDur(sec float64) string {
	if sec <= 0 {
		return ""
	}
	s := int64(sec + 0.5)
	if s < 3600 {
		return fmt.Sprintf("%d:%02d", s/60, s%60)
	}
	return fmt.Sprintf("%d:%02d:%02d", s/3600, (s/60)%60, s%60)
}

func parentOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// crumb is one breadcrumb segment.
type crumb struct {
	Name string
	Path string
}

func crumbs(p string) []crumb {
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]crumb, len(parts))
	for i := range parts {
		out[i] = crumb{Name: parts[i], Path: strings.Join(parts[:i+1], "/")}
	}
	return out
}

// sortLink rebuilds the current query with a new sort column, toggling
// direction when the column is already active.
func sortLink(values url.Values, col string) template.URL {
	v := url.Values{}
	for k, vs := range values {
		if k == "sort" || k == "desc" || k == "page" {
			continue
		}
		v[k] = vs
	}
	v.Set("sort", col)
	if values.Get("sort") == col && values.Get("desc") == "" {
		v.Set("desc", "1")
	}
	return template.URL("?" + v.Encode())
}

// render writes a page, or just its "content" block for htmx requests.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "no template "+page, http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	id, _ := IdentityFrom(r.Context())
	data["Identity"] = id
	data["Version"] = s.cfg.Version
	data["Path"] = r.URL.Path
	data["Query"] = r.URL.Query()
	if _, ok := data["Title"]; !ok {
		data["Title"] = strings.ToUpper(page[:1]) + page[1:]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := "layout"
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-Boosted") == "" {
		name = "content"
	}
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render", "page", page, "err", err)
	}
}
