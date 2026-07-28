package wbgt

import (
	"math"
	"time"
)

// cosSolarZenith returns the cosine of the solar zenith angle at time t for
// the given latitude and longitude (degrees, east positive), using the
// NOAA/Meeus low-precision solar position algorithm (position accurate to
// well under 0.1° for 1900–2100).
func cosSolarZenith(lat, lon float64, t time.Time) float64 {
	const rad = math.Pi / 180

	// Julian day and Julian century (Unix epoch = JD 2440587.5).
	jd := float64(t.UnixMilli())/86400000.0 + 2440587.5
	jc := (jd - 2451545.0) / 36525.0

	// Geometric mean longitude and anomaly of the sun (degrees).
	gml := math.Mod(280.46646+jc*(36000.76983+jc*0.0003032), 360)
	gma := 357.52911 + jc*(35999.05029-0.0001537*jc)
	// Eccentricity of Earth's orbit.
	ecc := 0.016708634 - jc*(0.000042037+0.0000001267*jc)
	// Sun's equation of center.
	eqc := math.Sin(rad*gma)*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(2*rad*gma)*(0.019993-0.000101*jc) +
		math.Sin(3*rad*gma)*0.000289
	// True and apparent longitude (degrees).
	stl := gml + eqc
	sal := stl - 0.00569 - 0.00478*math.Sin(rad*(125.04-1934.136*jc))
	// Obliquity, corrected (degrees).
	moe := 23 + (26+(21.448-jc*(46.815+jc*(0.00059-jc*0.001813)))/60)/60
	oc := moe + 0.00256*math.Cos(rad*(125.04-1934.136*jc))
	// Solar declination (radians).
	decl := math.Asin(math.Sin(rad*oc) * math.Sin(rad*sal))

	// Equation of time (minutes).
	vy := math.Tan(rad * oc / 2)
	vy *= vy
	eqTime := 4 / rad * (vy*math.Sin(2*rad*gml) -
		2*ecc*math.Sin(rad*gma) +
		4*ecc*vy*math.Sin(rad*gma)*math.Cos(2*rad*gml) -
		0.5*vy*vy*math.Sin(4*rad*gml) -
		1.25*ecc*ecc*math.Sin(2*rad*gma))

	// True solar time (minutes from local solar midnight).
	u := t.UTC()
	minutes := float64(u.Hour())*60 + float64(u.Minute()) +
		float64(u.Second())/60 + float64(u.Nanosecond())/6e10
	tst := math.Mod(minutes+eqTime+4*lon+1440, 1440)
	// Hour angle (degrees; 0 at solar noon).
	ha := tst/4 - 180
	if ha < -180 {
		ha += 360
	}

	return math.Sin(rad*lat)*math.Sin(decl) +
		math.Cos(rad*lat)*math.Cos(decl)*math.Cos(rad*ha)
}

// hourlyCosZenith samples the cosine zenith angle across the hour starting
// at start and returns cosza (average with night samples counted as zero)
// and coszda (average over sunlit samples only; 0 if the sun never rises
// within the hour). Sunlit-only averaging follows Kong & Huber (2022).
func hourlyCosZenith(lat, lon float64, start time.Time) (cosza, coszda float64) {
	const n = 12
	var sum, daySum float64
	var dayN int
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration((float64(i) + 0.5) / n * float64(time.Hour)))
		if c := cosSolarZenith(lat, lon, ts); c > 0 {
			sum += c
			daySum += c
			dayN++
		}
	}
	cosza = sum / n
	if dayN > 0 {
		coszda = daySum / float64(dayN)
	}
	return cosza, coszda
}
