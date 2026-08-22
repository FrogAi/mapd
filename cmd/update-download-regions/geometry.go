package main

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/clip"
	"github.com/paulmach/orb/planar"
)

// Must match settings.GROUP_AREA_BOX_DEGREES, which defines the runtime archive grid.
const archiveDegrees = 2

type coordinate struct {
	latitude  int
	longitude int
}

type archiveRange [3]int

func archivesForBounds(bounds orb.Bound) []coordinate {
	var coordinates []coordinate
	for latitude := archiveStart(bounds.Min[1]); latitude < archiveStop(bounds.Max[1]); latitude += archiveDegrees {
		for longitude := archiveStart(bounds.Min[0]); longitude < archiveStop(bounds.Max[0]); longitude += archiveDegrees {
			coordinates = append(coordinates, coordinate{latitude: latitude, longitude: longitude})
		}
	}
	return coordinates
}

func archivesForGeometry(polygons orb.MultiPolygon) []coordinate {
	selected := make(map[coordinate]struct{})
	for _, polygon := range polygons {
		bounds := polygon.Bound()
		for latitude := archiveStart(bounds.Min[1]); latitude < archiveStop(bounds.Max[1]); latitude += archiveDegrees {
			for longitude := archiveStart(bounds.Min[0]); longitude < archiveStop(bounds.Max[0]); longitude += archiveDegrees {
				archive := orb.Bound{
					Min: orb.Point{float64(longitude), float64(latitude)},
					Max: orb.Point{float64(longitude + archiveDegrees), float64(latitude + archiveDegrees)},
				}
				if polygonIntersectsBound(polygon, archive, bounds) {
					selected[coordinate{latitude: latitude, longitude: longitude}] = struct{}{}
				}
			}
		}
	}

	coordinates := make([]coordinate, 0, len(selected))
	for coordinate := range selected {
		coordinates = append(coordinates, coordinate)
	}
	sort.Slice(coordinates, func(first, second int) bool {
		if coordinates[first].latitude != coordinates[second].latitude {
			return coordinates[first].latitude < coordinates[second].latitude
		}
		return coordinates[first].longitude < coordinates[second].longitude
	})
	return coordinates
}

func scopeGeometry(polygons orb.MultiPolygon, bounds orb.Bound, path string) (orb.MultiPolygon, error) {
	if len(polygons) == 0 {
		return nil, fmt.Errorf("%s has no source geometry", path)
	}
	for _, polygon := range polygons {
		for _, ring := range polygon {
			if ringCrossesAntimeridian(ring) {
				return nil, fmt.Errorf("%s has unsupported source geometry", path)
			}
		}
	}

	seed := orb.Bound{
		Min: orb.Point{float64(archiveStart(bounds.Min[0])), float64(archiveStart(bounds.Min[1]))},
		Max: orb.Point{float64(archiveStop(bounds.Max[0])), float64(archiveStop(bounds.Max[1]))},
	}

	// Use the existing bounds to choose components, then keep each selected
	// component whole so normal border changes do not get clipped.
	selected := make(orb.MultiPolygon, 0, len(polygons))
	for _, polygon := range polygons {
		if polygonIntersectsBound(polygon, seed, polygon.Bound()) {
			selected = append(selected, polygon)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s does not intersect its bounding_box", path)
	}
	return selected, nil
}

func compactRanges(coordinates []coordinate) []archiveRange {
	var ranges []archiveRange
	for index := 0; index < len(coordinates); {
		latitude := coordinates[index].latitude
		minimumLongitude := coordinates[index].longitude
		maximumLongitude := minimumLongitude + archiveDegrees
		index++

		for index < len(coordinates) && coordinates[index].latitude == latitude && coordinates[index].longitude == maximumLongitude {
			maximumLongitude += archiveDegrees
			index++
		}
		ranges = append(ranges, archiveRange{latitude, minimumLongitude, maximumLongitude})
	}
	return ranges
}

func archiveStart(value float64) int {
	return int(math.Floor(value/archiveDegrees)) * archiveDegrees
}

func archiveStop(value float64) int {
	return int(math.Ceil(value/archiveDegrees)) * archiveDegrees
}

func coordinatesEqual(first, second []coordinate) bool {
	return slices.Equal(first, second)
}

func polygonIntersectsBound(polygon orb.Polygon, target, bounds orb.Bound) bool {
	if !bounds.Intersects(target) {
		return false
	}
	for _, ring := range polygon {
		if len(ring) == 1 && target.Contains(ring[0]) {
			return true
		}
		if len(ring) < 2 {
			continue
		}
		line := orb.LineString(ring).Clone()
		if line[0] != line[len(line)-1] {
			line = append(line, line[0])
		}
		if len(clip.LineString(target, line)) > 0 {
			return true
		}
	}

	for _, corner := range target.ToRing()[:4] {
		if planar.PolygonContains(polygon, corner) {
			return true
		}
	}
	return false
}

func ringCrossesAntimeridian(ring orb.Ring) bool {
	for index, start := range ring {
		if math.Abs(start[0]-ring[(index+1)%len(ring)][0]) > 180 {
			return true
		}
	}
	return false
}
