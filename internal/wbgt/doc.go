// Package wbgt estimates outdoor wet-bulb globe temperature (WBGT) when the
// NWS gridpoint wetBulbGlobeTemperature layer is unavailable. Values produced
// here are always labeled "estimated" (domain.SourceTag.Estimated).
//
// The implementation is a direct port of the zero-iteration analytic
// approximation of Liljegren's outdoor WBGT model:
//
//	Kong, Q., & Huber, M. (2024). A New, Zero-Iteration Analytic
//	Implementation of Wet-Bulb Globe Temperature: Development, Validation,
//	and Comparison With Other Methods. GeoHealth, 8(10), e2024GH001068.
//	https://doi.org/10.1029/2024GH001068
//
// (The plan cites this paper as "e2024GH001051"; the published article
// number is e2024GH001068.) The port follows the authors' reference code
// WBGT_analytic.py, archived at https://doi.org/10.5281/zenodo.10802580,
// which linearizes the wick and globe energy balances around first guesses
// (air temperature for the globe; Stull's 2011 wet-bulb formula for the
// wick) so no iteration is needed. Kong & Huber report agreement within
// 1 °C of the full iterative Liljegren model in >99% of ERA5 hours.
//
// The underlying physical model is:
//
//	Liljegren, J. C., Carhart, R. A., Lawday, P., Tschopp, S., & Sharp, R.
//	(2008). Modeling the wet bulb globe temperature using standard
//	meteorological measurements. Journal of Occupational and Environmental
//	Hygiene, 5(10), 645–655. https://doi.org/10.1080/15459620802310770
//
// Kong & Huber's functions take downwelling/upwelling longwave and reflected
// shortwave radiation as inputs (available in CMIP6/ERA5 but not from
// Open-Meteo), so those fields are parameterized exactly as in Liljegren's
// original code: atmospheric emissivity 0.575·ea^0.143 (ea in hPa), surface
// temperature equal to air temperature with emissivity 0.999, and reflected
// shortwave = 0.45·(global shortwave). The 10 m → 2 m wind conversion and
// the direct-beam fraction estimate (used only when Open-Meteo's
// direct/diffuse split is absent) are Kong & Huber's getwind2m and fdir.
//
// The cosine solar zenith angle is computed with the standard NOAA/Meeus
// solar position approximation and, following Kong & Huber (2022), averaged
// over the sunlit part of the hour (coszda) to avoid spurious WBGT spikes
// near sunrise and sunset.
package wbgt
