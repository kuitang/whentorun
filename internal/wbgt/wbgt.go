package wbgt

import (
	"math"
	"time"
)

// Sensor geometry and physical constants from Liljegren et al. (2008), as
// used in Kong & Huber (2024) WBGT_analytic.py.
const (
	diamGlobe = 0.0508 // globe diameter (m)
	emisGlobe = 0.95   // globe emissivity
	albGlobe  = 0.05   // globe albedo
	albSfc    = 0.45   // ground surface albedo

	emisWick = 0.95   // wick emissivity
	albWick  = 0.4    // wick albedo
	diamWick = 0.007  // wick diameter (m)
	lenWick  = 0.0254 // wick length (m)

	emisSfc = 0.999 // ground surface emissivity (Liljegren 2008)

	stefanB = 5.6696e-8   // Stefan–Boltzmann constant (W/(m² K⁴))
	mAir    = 28.97       // molecular weight of dry air (g/mol)
	mH2O    = 18.015      // molecular weight of water vapor (g/mol)
	rGas    = 8314.34     // gas constant (J/(kmol K))
	rAir    = rGas / mAir // gas constant of dry air (J/(kg K))
	cp      = 1003.5      // specific heat of air (J/(kg K))
	prandtl = cp / (cp + 1.25*rAir)

	stdPressurePa = 101325.0
	minWind2m     = 0.13   // m/s floor after 10 m → 2 m conversion
	solarConst    = 1367.0 // W/m²
	mphToMS       = 0.44704
)

// cosHorizon = cos(89.5°): below this sun elevation the direct fraction is 0.
var cosHorizon = math.Cos(89.5 * math.Pi / 180)

// Inputs are the hourly meteorological inputs for one path, in the units the
// rest of the app uses (°F, mph). All radiation values are hourly means on a
// horizontal plane. DirectWm2/DiffuseWm2 are Open-Meteo's split of SolarWm2;
// if both are zero while SolarWm2 > 0 the direct fraction is estimated with
// Kong & Huber's fdir formula instead.
type Inputs struct {
	TempF      float64   // 2 m air temperature
	DewPointF  float64   // 2 m dew point
	WindMPH    float64   // 10 m wind speed
	SolarWm2   float64   // global horizontal shortwave (Open-Meteo shortwave_radiation)
	DirectWm2  float64   // direct horizontal shortwave (Open-Meteo direct_radiation)
	DiffuseWm2 float64   // diffuse horizontal shortwave (Open-Meteo diffuse_radiation)
	PressurePa float64   // surface pressure; 0 means standard 101325 Pa
	Lat, Lon   float64   // degrees, east positive
	Time       time.Time // start of the hour the inputs describe
}

// EstimateF returns the estimated outdoor WBGT in °F via the Kong & Huber
// (2024) zero-iteration analytic Liljegren approximation.
func EstimateF(in Inputs) float64 {
	cosza, coszda := hourlyCosZenith(in.Lat, in.Lon, in.Time)
	return estimateF(in, cosza, coszda)
}

// estimateF is EstimateF with the zenith averages supplied by the caller
// (split out so tests can pin them).
func estimateF(in Inputs, cosza, coszda float64) float64 {
	tas := fToK(in.TempF)
	ps := in.PressurePa
	if ps <= 0 {
		ps = stdPressurePa
	}
	ea := esat(fToK(in.DewPointF), ps) // ambient vapor pressure (Pa)
	if es := esat(tas, ps); ea > es {
		ea = es // dew point cannot exceed air temperature
	}
	rsds := math.Max(in.SolarWm2, 0)
	f := directFraction(cosza, coszda, rsds, in.DirectWm2, in.DiffuseWm2)
	w2 := wind2m(math.Max(in.WindMPH, 0)*mphToMS, cosza, rsds)

	// Longwave and reflected-shortwave fields parameterized per Liljegren
	// et al. (2008): sky emissivity 0.575·ea^0.143 (ea in hPa), surface at
	// air temperature with emissivity 0.999, ground albedo 0.45.
	emisAtm := 0.575 * math.Pow(ea/100, 0.143)
	rlds := emisAtm * stefanB * pow4(tas)
	rlus := emisSfc * stefanB * pow4(tas)
	rsus := albSfc * rsds

	tg := globeTemp(tas, ps, w2, coszda, rsds, rlds, rsus, rlus, f)
	tnw := naturalWetBulb(tas, ea, ps, w2, coszda, rsds, rlds, rsus, rlus, f)
	return kToF(0.7*tnw + 0.2*tg + 0.1*tas)
}

// globeTemp is Kong & Huber's calc_Tg: the black-globe energy balance
// linearized around air temperature. All temperatures K, pressures Pa,
// radiation W/m², wind m/s.
func globeTemp(tas, ps, wind2m, coszda, rsds, rlds, rsus, rlus, f float64) float64 {
	hc := hSphereInAir(tas, ps, wind2m)
	hr := 4 * emisGlobe * stefanB * tas * tas * tas

	sr := 0.5 * (1 - albGlobe) * rsds * (1 - f)
	if f > 0 {
		sr += 0.5 * (1 - albGlobe) * rsds * 0.5 * f / coszda
	}
	sr += 0.5 * (1 - albGlobe) * rsus
	lr := 0.5*emisGlobe*(rlds+rlus) - stefanB*emisGlobe*pow4(tas)
	return tas + (sr+lr)/(hc+hr)
}

// naturalWetBulb is Kong & Huber's calc_Tnw: the wet-wick energy balance
// linearized around Stull's (2011) wet-bulb temperature.
func naturalWetBulb(tas, ea, ps, wind2m, coszda, rsds, rlds, rsus, rlus, f float64) float64 {
	es := esat(tas, ps)
	rh := ea / es * 100

	tw := stullWetBulb(tas, rh)
	tf := (tw + tas) / 2 // film temperature
	lv := heatOfEvap(tf)
	kx := convMass(tf, ps, wind2m)
	hc := hCylinderInAir(tf, ps, wind2m)
	beta := kx * mH2O * lv / (ps - esat(tw, ps))
	he := beta * desatDT(tf, ps)
	hr := stefanB * emisWick * (tw*tw + tas*tas) * (tw + tas)

	sr := (1 - albWick) * (1 + 0.25*diamWick/lenWick) * (1 - f) * rsds
	if f > 0 {
		sr += (1 - albWick) * (math.Tan(math.Acos(coszda))/math.Pi + 0.25*diamWick/lenWick) * f * rsds
	}
	sr += (1 - albWick) * rsus
	lr := 0.5*emisWick*(rlds+rlus) - stefanB*emisWick*pow4(tas)
	vpd := beta * (es - ea)
	return tas + (sr+lr-vpd)/(he+hc+hr)
}

// stullWetBulb is Stull's (2011) empirical wet-bulb temperature, used only
// as the linearization point. t in K, rh in %; returns K.
func stullWetBulb(t, rh float64) float64 {
	tc := t - 273.15
	return tc*math.Atan(0.151977*math.Sqrt(rh+8.313659)) +
		math.Atan(tc+rh) - math.Atan(rh-1.676331) +
		0.00391838*math.Pow(rh, 1.5)*math.Atan(0.023101*rh) -
		4.686035 + 273.15
}

// esat returns saturation vapor pressure (Pa) over water (t > 0 °C) or ice,
// with Buck's pressure enhancement factor. t in K, ps in Pa.
func esat(t, ps float64) float64 {
	if t > 273.15 {
		return (1.0007 + 3.46e-6*ps/100) * 611.21 * math.Exp(17.502*(t-273.15)/(t-32.18))
	}
	return (1.0003 + 4.18e-6*ps/100) * 611.15 * math.Exp(22.452*(t-273.15)/(t-0.6))
}

// desatDT is d(esat)/dT (Pa/K).
func desatDT(t, ps float64) float64 {
	if t > 273.15 {
		return esat(t, ps) * 17.502 * (273.15 - 32.18) / ((t - 32.18) * (t - 32.18))
	}
	return esat(t, ps) * 22.452 * (273.15 - 0.6) / ((t - 0.6) * (t - 0.6))
}

// heatOfEvap returns the latent heat of evaporation (J/kg) at t (K).
func heatOfEvap(t float64) float64 {
	return (313.15-t)/30*(-71100) + 2.4073e6
}

// viscosity returns dynamic viscosity of air (kg/(m s)) at t (K).
func viscosity(t float64) float64 {
	omega := 1.2945 - t/1141.176470588
	return 0.0000026693 * math.Sqrt(28.97*t) / (13.082689 * omega)
}

// thermCond returns thermal conductivity of air (W/(m K)) at t (K).
func thermCond(t float64) float64 {
	return (cp + 1.25*rAir) * viscosity(t)
}

// diffusivity returns diffusivity of water vapor in air (m²/s).
func diffusivity(t, ps float64) float64 {
	return 2.471773765165648e-05 * math.Pow(t*0.0034210563748421257, 2.334) * (101325 / ps)
}

// hCylinderInAir is the convective heat-transfer coefficient (W/(m² K)) for
// cross-flow around the wick cylinder.
func hCylinderInAir(t, ps, wind float64) float64 {
	density := ps / (rAir * t)
	re := wind * density * diamWick / viscosity(t)
	nu := 0.281 * math.Pow(re, 0.6) * math.Pow(prandtl, 0.44)
	return nu * thermCond(t) / diamWick
}

// hSphereInAir is the convective heat-transfer coefficient (W/(m² K)) for
// flow around the globe sphere.
func hSphereInAir(t, ps, wind float64) float64 {
	density := ps / (rAir * t)
	re := wind * density * diamGlobe / viscosity(t)
	nu := 2 + 0.6*math.Sqrt(re)*math.Pow(prandtl, 0.3333)
	return nu * thermCond(t) / diamGlobe
}

// convMass is the convective mass-transfer coefficient for the wick.
func convMass(t, ps, wind float64) float64 {
	h := hCylinderInAir(t, ps, wind)
	density := ps / (rAir * t)
	sc := viscosity(t) / (density * diffusivity(t, ps))
	return h / (cp * mAir) * math.Pow(prandtl/sc, 0.56)
}

// directFraction returns the fraction of rsds arriving as direct beam. With
// Open-Meteo's direct/diffuse split it is measured directly; otherwise it is
// estimated with Kong & Huber's fdir clearness-index fit. Zero at night or
// with the sun below 0.5° elevation.
func directFraction(cosza, coszda, rsds, direct, diffuse float64) float64 {
	if rsds <= 0 || cosza <= cosHorizon || coszda <= cosHorizon {
		return 0
	}
	var f float64
	if direct > 0 || diffuse > 0 {
		f = direct / (direct + diffuse)
	} else {
		sStar := rsds / (solarConst * coszda)
		if sStar > 0.85 {
			sStar = 0.85
		}
		f = math.Exp(3 - 1.34*sStar - 1.65/sStar)
	}
	return math.Min(math.Max(f, 0), 0.9)
}

// wind2m converts 10 m wind to 2 m (m/s) with Kong & Huber's getwind2m
// power law; the exponent encodes an urban stability class chosen from sun
// and wind (their getexp).
func wind2m(wind10m, cosz, rsds float64) float64 {
	w := wind10m * math.Pow(0.2, windExponent(cosz, wind10m, rsds))
	return math.Max(w, minWind2m)
}

func windExponent(cosz, wind, rsds float64) float64 {
	day := cosz > 0
	switch {
	// Strong insolation or moderate sun with matching wind: exponent 0.2.
	case day && rsds >= 925 && wind >= 5,
		day && rsds >= 675 && rsds < 925 && wind >= 5 && wind < 6,
		day && rsds >= 175 && rsds < 675 && wind >= 2 && wind < 5:
		return 0.2
	// Windy, weak sun, or windy night: 0.25.
	case day && rsds >= 675 && rsds < 925 && wind >= 6,
		day && rsds >= 175 && rsds < 675 && wind >= 5,
		day && rsds < 175,
		!day && wind >= 2.5:
		return 0.25
	// Strong sun, light wind (unstable): 0.15.
	case day && rsds >= 925 && wind < 5,
		day && rsds >= 675 && rsds < 925 && wind < 5,
		day && rsds >= 175 && rsds < 675 && wind < 2:
		return 0.15
	// Calm night (stable): 0.3.
	default:
		return 0.3
	}
}

func fToK(f float64) float64 { return (f-32)*5/9 + 273.15 }
func kToF(k float64) float64 { return (k-273.15)*9/5 + 32 }
func pow4(x float64) float64 { return x * x * x * x }
