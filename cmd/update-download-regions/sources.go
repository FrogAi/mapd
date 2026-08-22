package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	shp "github.com/jonas-p/go-shp"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
)

type source struct {
	name     string
	url      string
	filename string
	sha256   string
}

var countrySource = source{
	name:     "Natural Earth 10m Admin 0 countries",
	url:      "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/f1890d9f152c896d250a77557a5751a93d494776/geojson/ne_10m_admin_0_countries.geojson",
	filename: "ne_10m_admin_0_countries.geojson",
	sha256:   "239eec57ac17f100a11e2536cffc56752c318b50ae765b0918ff7aab4ce8f255",
}

var stateSource = source{
	name:     "Census TIGER/Line states",
	url:      "https://www2.census.gov/geo/tiger/TIGER2025/STATE/tl_2025_us_state.zip",
	filename: "tl_2025_us_state.zip",
	sha256:   "59a220888a8d9be8117c4fcd38f542bd02d81abf0d198c78113595ad540dd957",
}

var countrySelectors = map[string][2]string{
	"FR": {"ADM0_A3", "FRA"},
	"NO": {"ADM0_A3", "NOR"},
	"TW": {"ADM0_A3", "TWN"},
}

var stateCodeAliases = map[string]string{"GM": "GU"}

type regionGeometries map[string]orb.MultiPolygon

func fetchSource(specification source, cacheDirectory string) (string, error) {
	if err := os.MkdirAll(cacheDirectory, 0o755); err != nil {
		return "", err
	}
	destination := filepath.Join(cacheDirectory, specification.filename)
	if content, err := os.ReadFile(destination); err == nil && hashBytes(content) == specification.sha256 {
		return destination, nil
	}

	request, err := http.NewRequest(http.MethodGet, specification.url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "mapd-download-region-updater/1")

	client := &http.Client{Timeout: 120 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s download returned %s", specification.name, response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if hashBytes(content) != specification.sha256 {
		return "", fmt.Errorf("%s no longer matches its pinned hash", specification.name)
	}
	if err := os.WriteFile(destination, content, 0o644); err != nil {
		return "", err
	}
	return destination, nil
}

func hashBytes(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash)
}

func loadCountryGeometries(sourcePath string, codes []string) (regionGeometries, error) {
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	collection, err := geojson.UnmarshalFeatureCollection(content)
	if err != nil {
		return nil, err
	}

	geometries := make(regionGeometries, len(codes))
	for _, code := range codes {
		selector := [2]string{"ISO_A2", code}
		if specialSelector, exists := countrySelectors[code]; exists {
			selector = specialSelector
		}

		var matches []orb.MultiPolygon
		for _, feature := range collection.Features {
			value, exists := feature.Properties[selector[0]].(string)
			if !exists || value != selector[1] {
				continue
			}
			switch geometry := feature.Geometry.(type) {
			case orb.Polygon:
				matches = append(matches, orb.MultiPolygon{geometry})
			case orb.MultiPolygon:
				matches = append(matches, geometry)
			default:
				return nil, fmt.Errorf("nation.%s has unsupported GeoJSON geometry %T", code, geometry)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("nation.%s matched %d source features", code, len(matches))
		}
		geometries[code] = matches[0]
	}
	return geometries, nil
}

func loadStateGeometries(sourcePath string, codes []string) (regionGeometries, error) {
	reader, err := shp.OpenZip(sourcePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	codeField := -1
	for index, field := range reader.Fields() {
		if field.String() == "STUSPS" {
			codeField = index
			break
		}
	}
	if codeField == -1 {
		return nil, fmt.Errorf("Census source has no STUSPS field")
	}

	sourceGeometries := make(regionGeometries)
	for reader.Next() {
		_, shape := reader.Shape()
		shapePolygon, ok := shape.(*shp.Polygon)
		if !ok {
			return nil, fmt.Errorf("Census source contains %T instead of polygons", shape)
		}
		polygons, err := polygonsFromShape(shapePolygon)
		if err != nil {
			return nil, err
		}
		code := strings.TrimSpace(reader.Attribute(codeField))
		if _, exists := sourceGeometries[code]; exists {
			return nil, fmt.Errorf("Census source contains duplicate STUSPS %q", code)
		}
		sourceGeometries[code] = polygons
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}

	geometries := make(regionGeometries, len(codes))
	for _, menuCode := range codes {
		sourceCode := menuCode
		if alias, exists := stateCodeAliases[menuCode]; exists {
			sourceCode = alias
		}
		geometry, exists := sourceGeometries[sourceCode]
		if !exists {
			return nil, fmt.Errorf("us_state.%s has no source feature", menuCode)
		}
		geometries[menuCode] = geometry
	}
	return geometries, nil
}

func polygonsFromShape(shape *shp.Polygon) (orb.MultiPolygon, error) {
	var exteriors []orb.Ring
	var holes []orb.Ring
	for index, start := range shape.Parts {
		end := len(shape.Points)
		if index+1 < len(shape.Parts) {
			end = int(shape.Parts[index+1])
		}
		if int(start) < 0 || int(start) >= end || end > len(shape.Points) {
			return nil, fmt.Errorf("Census source contains invalid polygon parts")
		}
		ring := make(orb.Ring, end-int(start))
		for pointIndex, sourcePoint := range shape.Points[start:end] {
			ring[pointIndex] = orb.Point{sourcePoint.X, sourcePoint.Y}
		}
		if ring.Orientation() == orb.CW {
			exteriors = append(exteriors, ring)
		} else {
			holes = append(holes, ring)
		}
	}

	polygons := make(orb.MultiPolygon, len(exteriors))
	for index, exterior := range exteriors {
		polygons[index] = orb.Polygon{exterior}
	}
	for _, hole := range holes {
		owner := smallestContainingPolygon(polygons, hole[0])
		if owner == -1 {
			polygons = append(polygons, orb.Polygon{hole})
		} else {
			polygons[owner] = append(polygons[owner], hole)
		}
	}
	return polygons, nil
}

func smallestContainingPolygon(polygons orb.MultiPolygon, target orb.Point) int {
	owner := -1
	ownerArea := math.Inf(1)
	for index, polygon := range polygons {
		area := math.Abs(planar.Area(polygon[0]))
		if area < ownerArea && planar.RingContains(polygon[0], target) {
			owner = index
			ownerArea = area
		}
	}
	return owner
}
