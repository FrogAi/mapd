package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/paulmach/orb"
)

const downloadMenuPath = "settings/download_menu.json"

type menuBounds struct {
	MinLat float64 `json:"min_lat"`
	MinLon float64 `json:"min_lon"`
	MaxLat float64 `json:"max_lat"`
	MaxLon float64 `json:"max_lon"`
}

type menuLocation struct {
	BoundingBox menuBounds `json:"bounding_box"`
}

type (
	downloadMenu   map[string]map[string]menuLocation
	downloadRanges map[string]map[string][]archiveRange
)

type summary struct {
	locations int
	ranged    int
	legacy    int
	selected  int
}

func (summary summary) String() string {
	return fmt.Sprintf(
		"%d regions, %d with explicit ranges; %d legacy archive occurrences -> %d selected",
		summary.locations, summary.ranged, summary.legacy, summary.selected,
	)
}

func main() {
	write := flag.Bool("write", false, "write updated archive ranges to the download menu instead of checking them")
	flag.Parse()
	if err := updateDownloadMenu(*write); err != nil {
		log.Fatal(err)
	}
}

func updateDownloadMenu(write bool) error {
	rawMenu, err := os.ReadFile(downloadMenuPath)
	if err != nil {
		return err
	}
	menuDocument, err := parseJSONObject(rawMenu)
	if err != nil {
		return err
	}
	var menu downloadMenu
	if err := json.Unmarshal(rawMenu, &menu); err != nil {
		return err
	}

	cacheDirectory, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	ranges, summary, err := generateDownloadRanges(menu, filepath.Join(cacheDirectory, "mapd", "download-region-sources"))
	if err != nil {
		return err
	}

	newline := "\n"
	if bytes.Contains(rawMenu, []byte("\r\n")) {
		newline = "\r\n"
	}
	if err := inlineArchiveRanges(&menuDocument, ranges, newline); err != nil {
		return err
	}
	expected := append(renderJSONObject(menuDocument, 0, newline), newline...)
	changed := !bytes.Equal(rawMenu, expected)

	if changed && !write {
		return fmt.Errorf("download menu is stale (%s); run with --write", summary)
	}
	if changed {
		if err := os.WriteFile(downloadMenuPath, expected, 0o644); err != nil {
			return err
		}
		fmt.Printf("updated %s (%s)\n", downloadMenuPath, summary)
	} else {
		fmt.Printf("download menu is up to date (%s)\n", summary)
	}
	return nil
}

func generateDownloadRanges(menu downloadMenu, cacheDirectory string) (downloadRanges, summary, error) {
	countryCodes := sortedKeys(menu["nation"])
	stateCodes := sortedKeys(menu["us_state"])
	if len(countryCodes)+len(stateCodes) == 0 {
		return nil, summary{}, fmt.Errorf("download menu contains no nation or us_state regions")
	}

	countryPath, err := fetchSource(countrySource, cacheDirectory)
	if err != nil {
		return nil, summary{}, err
	}
	countryGeometries, err := loadCountryGeometries(countryPath, countryCodes)
	if err != nil {
		return nil, summary{}, err
	}

	statePath, err := fetchSource(stateSource, cacheDirectory)
	if err != nil {
		return nil, summary{}, err
	}
	stateGeometries, err := loadStateGeometries(statePath, stateCodes)
	if err != nil {
		return nil, summary{}, err
	}

	geometries := map[string]regionGeometries{
		"nation":   countryGeometries,
		"us_state": stateGeometries,
	}
	ranges := make(downloadRanges)
	var result summary
	for _, section := range []string{"nation", "us_state"} {
		for _, code := range sortedKeys(menu[section]) {
			path := section + "." + code
			bounds, err := menu[section][code].BoundingBox.bound()
			if err != nil {
				return nil, summary{}, fmt.Errorf("%s: %w", path, err)
			}
			legacy := archivesForBounds(bounds)
			scoped, err := scopeGeometry(geometries[section][code], bounds, path)
			if err != nil {
				return nil, summary{}, err
			}
			selected := archivesForGeometry(scoped)

			result.locations++
			result.legacy += len(legacy)
			result.selected += len(selected)
			if coordinatesEqual(selected, legacy) {
				continue
			}
			if ranges[section] == nil {
				ranges[section] = make(map[string][]archiveRange)
			}
			ranges[section][code] = compactRanges(selected)
			result.ranged++
		}
	}
	return ranges, result, nil
}

func (bounds menuBounds) bound() (orb.Bound, error) {
	result := orb.Bound{Min: orb.Point{bounds.MinLon, bounds.MinLat}, Max: orb.Point{bounds.MaxLon, bounds.MaxLat}}
	if result.Min[0] < -180 || result.Max[0] > 180 || result.Min[0] >= result.Max[0] || result.Min[1] < -90 || result.Max[1] > 90 || result.Min[1] >= result.Max[1] {
		return orb.Bound{}, fmt.Errorf("invalid bounding_box")
	}
	return result, nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
