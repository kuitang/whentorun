package domain

import "testing"

func TestCompassPoint(t *testing.T) {
	tests := []struct {
		deg  float64
		want string
	}{
		// The 16 cardinal centers.
		{0, "N"}, {22.5, "NNE"}, {45, "NE"}, {67.5, "ENE"},
		{90, "E"}, {112.5, "ESE"}, {135, "SE"}, {157.5, "SSE"},
		{180, "S"}, {202.5, "SSW"}, {225, "SW"}, {247.5, "WSW"},
		{270, "W"}, {292.5, "WNW"}, {315, "NW"}, {337.5, "NNW"},
		// Sector boundaries: each point spans ±11.25° around its center.
		{11.24, "N"}, {11.25, "NNE"}, {33.74, "NNE"}, {33.75, "NE"},
		{348.74, "NNW"}, {348.75, "N"}, {359.9, "N"},
		// Off-center values inside a sector.
		{10, "N"}, {200, "SSW"}, {190, "S"}, {170, "S"},
		// Normalization: full turns and negatives.
		{360, "N"}, {720, "N"}, {450, "E"}, {-90, "W"}, {-11.25, "N"}, {-11.26, "NNW"},
	}
	for _, tt := range tests {
		if got := CompassPoint(tt.deg); got != tt.want {
			t.Errorf("CompassPoint(%v) = %q, want %q", tt.deg, got, tt.want)
		}
	}
}
