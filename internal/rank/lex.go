package rank

import "github.com/kuitang/whentorun/internal/domain"

// Coarse categorical buckets, then lexicographic comparison. Buckets keep
// the ranking transparent and prevent meaningless precision from deciding
// ("72.1°F WBGT beats 72.3°F"): hours only separate when an official
// category (or a coarse band of a raw metric) actually differs.
//
// Lower bucket value is always better. A missing metric sorts WORST for its
// key (labelled "unknown") so an hour with unknown air quality can never
// beat an hour with known-clean air.

// unknownBucketVal ranks missing data below every known bucket.
const unknownBucketVal = 99

type bucket struct {
	v     int
	label string
}

var unknownBucket = bucket{unknownBucketVal, "unknown"}

// bucketOf places v into the band below the first cut it is less than.
// len(labels) == len(cuts)+1.
func bucketOf(v float64, cuts []float64, labels []string) bucket {
	for i, c := range cuts {
		if v < c {
			return bucket{i, labels[i]}
		}
	}
	return bucket{len(cuts), labels[len(cuts)]}
}

// wbgtBucket: NWS WBGT flag bands (°F): <80 / 80–85 / 85–88 / 88–90 / >=90.
func wbgtBucket(m domain.Metric) bucket {
	if !m.Valid {
		return unknownBucket
	}
	return bucketOf(m.Value, []float64{80, 85, 88, 90},
		[]string{"low heat stress", "elevated heat stress", "high heat stress", "very high heat stress", "extreme heat stress"})
}

// dewBucket: runner dew-point bands (°F): <55 / 55–60 / 60–65 / 65–70 / 70–75 / >=75.
func dewBucket(m domain.Metric) bucket {
	if !m.Valid {
		return unknownBucket
	}
	return bucketOf(m.Value, []float64{55, 60, 65, 70, 75},
		[]string{"dry", "comfortable", "sticky", "humid", "oppressive", "miserable"})
}

// aqiBucket: official EPA AQI categories.
func aqiBucket(m domain.Metric) bucket {
	if !m.Valid {
		return unknownBucket
	}
	return aqiBucketVal(m.Value)
}

func aqiBucketVal(v float64) bucket {
	return bucketOf(v, []float64{51, 101, 151, 201, 301},
		[]string{"Good", "Moderate", "Unhealthy for Sensitive Groups", "Unhealthy", "Very Unhealthy", "Hazardous"})
}

// uvBucket: EPA UV Index bands: 0–2 / 3–5 / 6–7 / 8–10 / 11+.
func uvBucket(m domain.Metric) bucket {
	if !m.Valid {
		return unknownBucket
	}
	return bucketOf(m.Value, []float64{3, 6, 8, 11},
		[]string{"low", "moderate", "high", "very high", "extreme"})
}

// popBucket: coarse precipitation-probability bands (%): <20 / 20–49 / 50–69 / >=70.
func popBucket(m domain.Metric) bucket {
	if !m.Valid {
		return unknownBucket
	}
	return bucketOf(m.Value, []float64{20, 50, 70},
		[]string{"unlikely", "possible", "likely", "very likely"})
}

// windBucket uses gusts when available (gusts are what runners feel), else
// sustained wind (mph): <10 / 10–19 / 20–29 / >=30.
func windBucket(h domain.Hour) bucket {
	m := h.GustMPH
	if !m.Valid {
		m = h.WindMPH
	}
	if !m.Valid {
		return unknownBucket
	}
	return bucketOf(m.Value, []float64{10, 20, 30},
		[]string{"calm", "breezy", "windy", "very windy"})
}

// chillBucket: runner wind-chill bands (°F), wind chill falling back to air
// temperature. Higher is better here, so bands are explicit.
func chillBucket(h domain.Hour) bucket {
	m := h.WindChillF
	if !m.Valid {
		m = h.TempF
	}
	if !m.Valid {
		return unknownBucket
	}
	switch v := m.Value; {
	case v >= 32:
		return bucket{0, "mild"}
	case v >= 20:
		return bucket{1, "chilly"}
	case v >= 10:
		return bucket{2, "cold"}
	case v >= 0:
		return bucket{3, "very cold"}
	default:
		return bucket{4, "bitter"}
	}
}

// popIceBucket: cold-season precipitation key — any icy precipitation is its
// own worst band (it is also a veto), otherwise the PoP band.
func popIceBucket(h domain.Hour) bucket {
	if h.WxFreezingRain || (h.IceAccumIn.Valid && h.IceAccumIn.Value > 0) {
		return bucket{10, "icy precipitation"}
	}
	return popBucket(h.PoP)
}

// lexKey is one named, bucketed comparison key.
type lexKey struct {
	name string
	b    bucket
}

// warmKeys: WBGT category, dew point band, AQI category, UV band, PoP band,
// wind band — in that priority order.
func warmKeys(h domain.Hour) []lexKey {
	return []lexKey{
		{"heat stress (WBGT)", wbgtBucket(h.WBGTF)},
		{"dew point", dewBucket(h.DewPointF)},
		{"air quality (AQI)", aqiBucket(h.AQI)},
		{"UV", uvBucket(h.UVIndex)},
		{"rain chance", popBucket(h.PoP)},
		{"wind", windBucket(h)},
	}
}

// coldKeys: wind-chill band, precipitation/ice, AQI category, wind band.
func coldKeys(h domain.Hour) []lexKey {
	return []lexKey{
		{"wind chill", chillBucket(h)},
		{"precipitation", popIceBucket(h)},
		{"air quality (AQI)", aqiBucket(h.AQI)},
		{"wind", windBucket(h)},
	}
}

func keysFor(season Season, h domain.Hour) []lexKey {
	if season == SeasonCold {
		return coldKeys(h)
	}
	return warmKeys(h)
}

// KeyDiff reports the first lexicographic key on which two hours differed,
// with the category labels on each side, so the UI can explain a choice
// ("chosen because heat stress is one category lower").
type KeyDiff struct {
	Key string // human key name, e.g. "air quality (AQI)"
	A   string // category label for the first argument
	B   string // category label for the second argument
}

// CompareHours lexicographically compares two hours under the season's key
// order. Returns -1 if a ranks better than b, +1 if worse, 0 if every
// bucket ties; the KeyDiff names the first key that differed (zero on tie).
func CompareHours(season Season, a, b domain.Hour) (int, KeyDiff) {
	ka, kb := keysFor(season, a), keysFor(season, b)
	for i := range ka {
		if ka[i].b.v == kb[i].b.v {
			continue
		}
		d := KeyDiff{Key: ka[i].name, A: ka[i].b.label, B: kb[i].b.label}
		if ka[i].b.v < kb[i].b.v {
			return -1, d
		}
		return 1, d
	}
	return 0, KeyDiff{}
}

// primaryBucket is the season's first-key bucket value, used for window
// extension ("stays in the same top bucket").
func primaryBucket(season Season, h domain.Hour) int {
	if season == SeasonCold {
		return chillBucket(h).v
	}
	return wbgtBucket(h.WBGTF).v
}
