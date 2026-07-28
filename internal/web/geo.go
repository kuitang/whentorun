package web

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/kuitang/whentorun/internal/domain"
)

const earthRadiusKm = 6371.0

// haversineKm returns the great-circle distance between two points.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLon := rad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(a))
}

// nearestPath returns the fixed path closest to (lat, lon).
func nearestPath(lat, lon float64) (domain.Path, float64) {
	best := domain.Paths[0]
	bestD := math.Inf(1)
	for _, p := range domain.Paths {
		if d := haversineKm(lat, lon, p.Lat, p.Lon); d < bestD {
			best, bestD = p, d
		}
	}
	return best, bestD
}

func (s *server) handleNearest(w http.ResponseWriter, r *http.Request) {
	lat, errLat := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	if errLat != nil || errLon != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		http.Error(w, "lat and lon are required", http.StatusBadRequest)
		return
	}
	p, d := nearestPath(lat, lon)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	resp := struct {
		Slug       string  `json:"slug"`
		Name       string  `json:"name"`
		DistanceKm float64 `json:"distance_km"`
	}{p.Slug, p.Name, math.Round(d*10) / 10}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("nearest: encode: %v", err)
	}
}
