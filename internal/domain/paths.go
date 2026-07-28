package domain

// Path is one of the fixed NYC running paths. Gridpoints were live-verified
// against api.weather.gov on 2026-07-28 (see PLAN.md); we hardcode them so
// no runtime /points lookup is needed.
type Path struct {
	Slug      string
	Name      string
	Lat, Lon  float64
	GridID    string // NWS forecast office
	GridX     int
	GridY     int
	AlertZone string // NWS public zone for alert polling
	// RouteNotes are static context: shade, wind exposure, flood-prone segments.
	RouteNotes string
}

var Paths = []Path{
	{
		Slug: "central-park", Name: "Central Park",
		Lat: 40.7829, Lon: -73.9654,
		GridID: "OKX", GridX: 34, GridY: 45, AlertZone: "NYZ072",
		RouteNotes: "Good tree shade on the loop's east and north sides; the reservoir path is exposed. Hills concentrate at the north end. Drains well after rain.",
	},
	{
		Slug: "hudson-greenway", Name: "Hudson River Greenway",
		Lat: 40.7570, Lon: -74.0046,
		GridID: "OKX", GridX: 33, GridY: 43, AlertZone: "NYZ072",
		RouteNotes: "Almost no shade; fully exposed to westerly river wind — headwind one way, tailwind back. Low-lying segments near piers can pond in heavy rain.",
	},
	{
		Slug: "east-river", Name: "East River Esplanade",
		Lat: 40.7331, Lon: -73.9741,
		GridID: "OKX", GridX: 34, GridY: 43, AlertZone: "NYZ072",
		RouteNotes: "Minimal shade; morning sun comes straight off the water. Stuyvesant Cove and the lower esplanade flood in coastal storms.",
	},
	{
		Slug: "brooklyn", Name: "Brooklyn — Prospect Park & waterfront",
		Lat: 40.6602, Lon: -73.9690,
		GridID: "OKX", GridX: 34, GridY: 41, AlertZone: "NYZ075",
		RouteNotes: "Prospect Park loop is well shaded with one long hill; the Brooklyn Bridge Park waterfront is exposed and breezy. Park drains slowly at the Nethermead after storms.",
	},
	{
		Slug: "van-cortlandt", Name: "Van Cortlandt Park",
		Lat: 40.8973, Lon: -73.8860,
		GridID: "OKX", GridX: 36, GridY: 51, AlertZone: "NYZ073",
		RouteNotes: "Deep shade on the trails and back hills; the parade ground is open. Cross-country trails hold mud for a day after rain; runs a touch cooler than Manhattan.",
	},
}

// PathBySlug returns the path with the given slug, or nil.
func PathBySlug(slug string) *Path {
	for i := range Paths {
		if Paths[i].Slug == slug {
			return &Paths[i]
		}
	}
	return nil
}

// AlertZones returns the deduplicated set of zones across all paths.
func AlertZones() []string {
	seen := map[string]bool{}
	var zones []string
	for _, p := range Paths {
		if !seen[p.AlertZone] {
			seen[p.AlertZone] = true
			zones = append(zones, p.AlertZone)
		}
	}
	return zones
}
