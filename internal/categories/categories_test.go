package categories

import (
	"math"
	"testing"
)

func TestWBGT(t *testing.T) {
	tests := []struct {
		f    float64
		want Category
	}{
		{-20, Category{"Low", 0}},
		{60, Category{"Low", 0}},
		{79.9, Category{"Low", 0}},
		{80.0, Category{"Elevated", 1}},
		{84.9, Category{"Elevated", 1}},
		{85.0, Category{"Moderate", 2}},
		{87.9, Category{"Moderate", 2}},
		{88.0, Category{"High", 3}},
		{89.9, Category{"High", 3}},
		{90.0, Category{"Extreme", 4}},
		{105, Category{"Extreme", 4}},
	}
	for _, tt := range tests {
		if got := WBGT(tt.f); got != tt.want {
			t.Errorf("WBGT(%v) = %+v, want %+v", tt.f, got, tt.want)
		}
	}
}

func TestDewPoint(t *testing.T) {
	tests := []struct {
		f    float64
		want Category
	}{
		{20, Category{"Pleasant", 0}},
		{54.9, Category{"Pleasant", 0}},
		{55.0, Category{"Comfortable", 1}},
		{59.9, Category{"Comfortable", 1}},
		{60.0, Category{"Sticky", 2}},
		{64.9, Category{"Sticky", 2}},
		{65.0, Category{"Uncomfortable", 3}},
		{69.9, Category{"Uncomfortable", 3}},
		{70.0, Category{"Oppressive", 4}},
		{74.9, Category{"Oppressive", 4}},
		{75.0, Category{"Miserable", 5}},
		{82, Category{"Miserable", 5}},
	}
	for _, tt := range tests {
		if got := DewPoint(tt.f); got != tt.want {
			t.Errorf("DewPoint(%v) = %+v, want %+v", tt.f, got, tt.want)
		}
	}
}

func TestUVIndex(t *testing.T) {
	tests := []struct {
		v    float64
		want Category
	}{
		{0, Category{"Low", 0}},
		{2, Category{"Low", 0}},
		{2.9, Category{"Low", 0}},
		{3.0, Category{"Moderate", 1}},
		{5, Category{"Moderate", 1}},
		{5.9, Category{"Moderate", 1}},
		{6.0, Category{"High", 2}},
		{7, Category{"High", 2}},
		{7.9, Category{"High", 2}},
		{8.0, Category{"Very High", 3}},
		{10, Category{"Very High", 3}},
		{10.9, Category{"Very High", 3}},
		{11.0, Category{"Extreme", 4}},
		{14, Category{"Extreme", 4}},
	}
	for _, tt := range tests {
		if got := UVIndex(tt.v); got != tt.want {
			t.Errorf("UVIndex(%v) = %+v, want %+v", tt.v, got, tt.want)
		}
	}
}

func TestAQI(t *testing.T) {
	tests := []struct {
		v    float64
		want Category
	}{
		{0, Category{"Good", 0}},
		{50, Category{"Good", 0}},
		{51, Category{"Moderate", 1}},
		{100, Category{"Moderate", 1}},
		{101, Category{"Unhealthy for Sensitive Groups", 2}},
		{150, Category{"Unhealthy for Sensitive Groups", 2}},
		{151, Category{"Unhealthy", 3}},
		{160, Category{"Unhealthy", 3}}, // brief's canary AQI must read as Unhealthy
		{200, Category{"Unhealthy", 3}},
		{201, Category{"Very Unhealthy", 4}},
		{300, Category{"Very Unhealthy", 4}},
		{301, Category{"Hazardous", 5}},
		{500, Category{"Hazardous", 5}},
	}
	for _, tt := range tests {
		if got := AQI(tt.v); got != tt.want {
			t.Errorf("AQI(%v) = %+v, want %+v", tt.v, got, tt.want)
		}
	}
}

func TestWindChill(t *testing.T) {
	tests := []struct {
		f    float64
		want Category
	}{
		{45, Category{"Manageable", 0}},
		{25.0, Category{"Manageable", 0}},
		{24.9, Category{"Cold", 1}},
		{10.0, Category{"Cold", 1}},
		{9.9, Category{"Very Cold", 2}},
		{0, Category{"Very Cold", 2}},
		{-10.0, Category{"Very Cold", 2}},
		{-10.1, Category{"Dangerous", 3}},
		{-30, Category{"Dangerous", 3}},
	}
	for _, tt := range tests {
		if got := WindChill(tt.f); got != tt.want {
			t.Errorf("WindChill(%v) = %+v, want %+v", tt.f, got, tt.want)
		}
	}
}

// TestComputeWindChill checks the formula against values from the official
// NWS wind chill chart (https://www.weather.gov/safety/cold-wind-chill-chart),
// which rounds to whole degrees — so we allow ±0.5 °F.
func TestComputeWindChill(t *testing.T) {
	tests := []struct {
		tempF, windMPH float64
		want           float64 // NWS chart value
	}{
		{40, 5, 36},
		{30, 10, 21},
		{20, 15, 6},
		{10, 20, -9},
		{0, 15, -19},
		{0, 35, -27},
		{-10, 25, -37},
		{-20, 30, -53},
		{40, 10, 34},
		{-45, 60, -98},
	}
	for _, tt := range tests {
		got, ok := ComputeWindChill(tt.tempF, tt.windMPH)
		if !ok {
			t.Errorf("ComputeWindChill(%v, %v): ok = false, want true", tt.tempF, tt.windMPH)
			continue
		}
		if math.Abs(got-tt.want) > 0.5 {
			t.Errorf("ComputeWindChill(%v, %v) = %v, want %v ±0.5", tt.tempF, tt.windMPH, got, tt.want)
		}
	}
}

func TestComputeWindChillValidity(t *testing.T) {
	tests := []struct {
		name           string
		tempF, windMPH float64
		wantOK         bool
	}{
		{"temp above 50", 50.1, 10, false},
		{"temp exactly 50", 50, 10, true},
		{"wind exactly 3", 20, 3, false},
		{"wind just above 3", 20, 3.1, true},
		{"calm", 20, 0, false},
		{"warm and calm", 70, 0, false},
	}
	for _, tt := range tests {
		got, ok := ComputeWindChill(tt.tempF, tt.windMPH)
		if ok != tt.wantOK {
			t.Errorf("%s: ComputeWindChill(%v, %v) ok = %v, want %v", tt.name, tt.tempF, tt.windMPH, ok, tt.wantOK)
		}
		if !ok && got != tt.tempF {
			t.Errorf("%s: out-of-range ComputeWindChill(%v, %v) = %v, want air temp %v back", tt.name, tt.tempF, tt.windMPH, got, tt.tempF)
		}
	}
}
