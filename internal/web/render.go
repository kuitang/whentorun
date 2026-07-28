package web

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/view"
)

// pathShortNames are the route-slider chip labels.
var pathShortNames = map[string]string{
	"central-park":    "Central Park",
	"hudson-greenway": "Hudson Greenway",
	"east-river":      "East River",
	"brooklyn":        "Brooklyn",
	"van-cortlandt":   "Van Cortlandt",
}

// glyphIDs whitelists the engraved glyph names templates may reference.
var glyphIDs = map[string]bool{
	"sun": true, "moon": true, "cloud": true, "rain": true, "storm": true,
	"sunrise": true, "sunset": true, "gibbous": true, "horizon": true,
}

func (s *server) funcs() template.FuncMap {
	return template.FuncMap{
		"asset": s.assetURL,

		// class helpers — leading space so they append to a class list
		"qclass": func(q int) string {
			if q >= 0 && q <= 2 {
				return fmt.Sprintf(" q%d", q)
			}
			return ""
		},
		"qspan": func(q int) string {
			if q >= 0 && q <= 2 {
				return fmt.Sprintf("q%d", q)
			}
			return ""
		},
		"ttclass": func(tier int) string {
			if tier >= 0 && tier <= 4 {
				return fmt.Sprintf(" tt%d", tier)
			}
			return ""
		},
		"twclass": func(tier int) string {
			if tier >= 0 && tier <= 4 {
				return fmt.Sprintf("tw%d", tier)
			}
			return ""
		},

		// vane rotation: wind FROM deg → arrow pointing where it blows
		"vane": func(deg float64) template.HTMLAttr {
			if deg < 0 {
				return ""
			}
			rot := math.Mod(deg-180+360, 360)
			if rot == 0 {
				return ""
			}
			return template.HTMLAttr(fmt.Sprintf(` style="transform:rotate(%.0fdeg)"`, rot))
		},

		"glyph": func(name string) string {
			if !glyphIDs[name] {
				name = "sun"
			}
			return "#g-" + name
		},

		// deg renders a temperature cell value with its degree mark
		"deg": func(m view.MetricVM) template.HTML {
			if !m.Valid() {
				return "&mdash;"
			}
			return template.HTML(template.HTMLEscapeString(m.Display) + "&deg;")
		},

		"dayShort": func(label string) string {
			if len(label) < 3 {
				return strings.ToUpper(label)
			}
			return strings.ToUpper(label[:3])
		},
		"pathShort": func(slug string) string {
			if n, ok := pathShortNames[slug]; ok {
				return n
			}
			return slug
		},

		// fetched renders a freshness stamp for a SourceVM or a time.Time
		"fetched": func(v any) string {
			switch t := v.(type) {
			case view.SourceVM:
				s := view.FmtFetched(t.FetchedAt)
				if !t.Fresh && !t.FetchedAt.IsZero() {
					s += " (stale)"
				}
				return s
			case time.Time:
				return view.FmtFetched(t)
			default:
				return "unavailable"
			}
		},

		"themeGlyph": func(theme string) string {
			switch theme {
			case "light":
				return "sun"
			case "dark":
				return "moon"
			default:
				return "sun-half"
			}
		},
		"themeAria": func(theme string) string {
			switch theme {
			case "light":
				return "Theme: light. Switch to dark."
			case "dark":
				return "Theme: dark. Switch to automatic."
			default:
				return "Theme: automatic, follows your system. Switch to light."
			}
		},
		"unitAria": func(units string) string {
			if units == "C" {
				return "Units: Celsius shown. Switch to Fahrenheit."
			}
			return "Units: Fahrenheit shown. Switch to Celsius."
		},

		// chartNote: "3 PM Tue → 2 PM Thu" over the 48 h span
		"chartNote": func(p view.Page) string {
			start := p.GeneratedAt.In(view.NYLoc).Truncate(time.Hour)
			end := start.Add(47 * time.Hour)
			f := func(t time.Time) string { return t.Format("3 PM Mon") }
			return f(start) + " → " + f(end)
		},
		"dateLock": func(t time.Time) string {
			return strings.ToUpper(t.In(view.NYLoc).Format("Mon Jan 2"))
		},

		// windowSub: the detail line under the next-best windows
		"windowSub": func(w view.WindowsVM) string {
			var parts []string
			add := func(prefix string, wv view.WindowVM) {
				switch {
				case !wv.Found():
				case wv.Vetoed:
					parts = append(parts, prefix+" is out — "+wv.Reason)
				case wv.Detail != "":
					parts = append(parts, prefix+": "+wv.Detail)
				}
			}
			add("Dawn", w.BeforeWork)
			add("Evening", w.AfterWork)
			return strings.Join(parts, " — ")
		},
	}
}

func (s *server) assetURL(name string) string {
	return s.assets.URL(name)
}
