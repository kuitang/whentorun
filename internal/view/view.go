// Package view is the shared viewmodel contract between the merge/rank
// backend and the HTML templates. It is pure data + formatting: no I/O, no
// HTTP. internal/web builds a Page per request and hands it to the
// templates; sibling packages (chart, narrative) fill ChartSVG and
// Narrative via hooks in internal/web.
package view

import (
	"html/template"
	"time"

	"github.com/kuitang/whentorun/internal/chart"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
)

// Page is everything one SSR page render needs.
type Page struct {
	Path  domain.Path
	Paths []domain.Path
	Units string // "F" | "C"
	Theme string // "" (system) | "light" | "dark"

	Now           NowVM
	Alerts        []AlertVM
	AlertFeedDown bool

	Narrative   string
	PeriodProse []ProseVM
	Windows     WindowsVM
	Days        []DayVM
	ChartSVG    template.HTML
	Sun         []merge.SunTimes
	Freshness   []SourceVM
	GeneratedAt time.Time

	// Additions to the shared contract (noted for the Integrate stage):
	// Clothing is the one-line clothing note (rule-based fallback until
	// the narrative engine supplies it); ChartMeta carries the sticky
	// y-axis labels the fig template renders beside ChartSVG.
	Clothing  string
	ChartMeta chart.Meta
}

// BreakVM is one narrative divider row destined for the hourly ledger: it
// renders after the hour row starting at After (mirrors narrative.Break
// without importing it).
type BreakVM struct {
	After time.Time
	Text  string
}

// NowVM is the compressed current-conditions panel.
type NowVM struct {
	TimeLabel string // "2:30 PM ET"

	WBGT      MetricVM
	Temp, Dew MetricVM

	UV, AQI, RH, Wind, Rain MetricVM

	AQIPollutant string // dominant pollutant, e.g. "PM2.5"
	WindCompass  string // 16-point compass, e.g. "S"
	WBGTPhrase   string // "High heat stress"

	// Additions to the shared contract (noted for Integrate): DewNote is
	// the italic aside under the dew figure ("oppressive at a jog");
	// UVWord/AQIWord are the strip's category words; WindDeg feeds the
	// vane rotation (-1 unknown).
	DewNote string
	UVWord  string
	AQIWord string
	WindDeg float64
}

// MetricVM is one formatted metric value.
type MetricVM struct {
	Display string // formatted for the current units; "—" when missing
	Tier    int    // WBGT heat band 0..4; -1 when n/a (all other metrics)
	Quality int    // 3-state quality: 0 good / 1 fair / 2 poor; -1 n/a
	Source  domain.SourceTag
}

// Valid reports whether the metric carries a real value.
func (m MetricVM) Valid() bool { return m.Display != "" && m.Display != missingDisplay }

// DayVM is one calendar day of the hourly ledger.
type DayVM struct {
	Label    string // "Tuesday July 28 · today"
	SunLabel string // "rises 5:48 · sets 8:15" ("" when sun data missing)
	Rows     []RowVM
}

// RowVM is one row of the hourly ledger.
type RowVM struct {
	Kind string // "hour" | "sun" | "narrative" | "window-ann"

	Time      time.Time
	TimeLabel string // "3 PM" (non-breaking space)
	Glyph     string // engraved glyph id suffix: sun, moon, cloud, rain, storm, sunrise, sunset

	WBGT, Temp, Dew, RH, UV, AQI, Wind, Rain MetricVM

	WindDeg float64 // meteorological degrees (wind FROM); -1 when unknown
	Veto    string  // named veto reason(s) or ""
	Window  string  // "" | "before-work" | "after-work"
	Thunder bool

	// Text: sun/narrative rows ("sunrise 5:49"); on the first hour row of a
	// best window it carries the annotation ("before work").
	Text string
}

// WindowsVM frames the two commuter windows.
type WindowsVM struct {
	BeforeWork WindowVM
	AfterWork  WindowVM
	Lede       string
}

// WindowVM is one recommended (or vetoed) window.
type WindowVM struct {
	Label  string // "Before work" | "After work"
	Range  string // "Wed 5–9 AM"
	Detail string // "74–78° elevated · UV 2 · AQI 50 good"
	Vetoed bool
	Reason string // named veto reasons, "" when not vetoed
}

// Found reports whether a window was identified at all.
func (w WindowVM) Found() bool { return w.Range != "" }

// ProseVM is one NWS period-forecast prose block.
type ProseVM struct {
	Name   string
	Text   string
	Source domain.SourceTag
}

// SourceVM is one upstream feed's freshness for the footer/explainers.
type SourceVM struct {
	Name      string
	FetchedAt time.Time
	Fresh     bool
}

// AlertVM is one active alert for the banner.
type AlertVM struct {
	Event string // "Heat Advisory"
	Note  string // "until 8 PM tonight."
}
