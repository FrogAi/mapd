package math

import (
	m "math"
)

type Curvature struct {
	Curvature, ArcLength, Angle float64
	Pos                         Position
}

func CalculateCurvature(a Position, b Position, c Position) Curvature {
	distanceAB := a.distanceTo(b)
	distanceAC := a.distanceTo(c)
	distanceBC := b.distanceTo(c)
	distanceProduct := distanceAB * distanceAC * distanceBC
	if distanceProduct == 0 {
		return Curvature{Pos: b}
	}

	semiperimeter := (distanceAB + distanceAC + distanceBC) / 2
	areaSquared := semiperimeter * (semiperimeter - distanceAB) * (semiperimeter - distanceAC) * (semiperimeter - distanceBC)
	// Rounding can make the squared area slightly negative for nearly straight roads.
	res := Curvature{Pos: b}
	res.Curvature = 4 * m.Sqrt(max(0, areaSquared)) / distanceProduct
	if res.Curvature == 0 {
		res.ArcLength = distanceAC
		return res
	}

	res.Angle = 2 * m.Asin(min(1, distanceAC*res.Curvature/2))
	res.ArcLength = res.Angle / res.Curvature
	return res
}
