package converter

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/BryanSmee/bl_to_std/pkg/paint"
)

// objectMeshes records where a built root object's geometry lives.
type objectMeshes struct {
	meshPaths   []string // component mesh files (zip names)
	inlinePaint []string // paint_color of inline triangles, if any
	extruder    int      // base extruder (filament number), default 1
}

type rootModelXML struct {
	Objects []struct {
		ID         int `xml:"id,attr"`
		Components []struct {
			Path     string `xml:"path,attr"`
			ObjectID int    `xml:"objectid,attr"`
		} `xml:"components>component"`
		InlineTriangles []struct {
			Paint string `xml:"paint_color,attr"`
		} `xml:"mesh>triangles>triangle"`
	} `xml:"resources>object"`
	Items []struct {
		ObjectID int `xml:"objectid,attr"`
	} `xml:"build>item"`
}

type modelSettingsXML struct {
	Objects []struct {
		ID   int `xml:"id,attr"`
		Meta []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:"value,attr"`
		} `xml:"metadata"`
	} `xml:"object"`
}

// analyzeObjects maps each built root object to its meshes/extruder and
// returns the set of all object-mesh zip paths (for routing during writing).
func analyzeObjects(zr *zip.Reader) (map[int]*objectMeshes, map[string]bool, error) {
	rootData, err := readZipFile(zr, rootModelPath)
	if err != nil {
		return nil, nil, err
	}
	if rootData == nil {
		return nil, nil, fmt.Errorf("missing %s", rootModelPath)
	}
	var root rootModelXML
	if err := xml.Unmarshal(rootData, &root); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", rootModelPath, err)
	}

	extruders := map[int]int{}
	if msData, err := readZipFile(zr, modelSettingsPath); err != nil {
		return nil, nil, err
	} else if msData != nil {
		var ms modelSettingsXML
		if err := xml.Unmarshal(msData, &ms); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", modelSettingsPath, err)
		}
		for _, o := range ms.Objects {
			for _, m := range o.Meta {
				if m.Key == "extruder" {
					if v, err := strconv.Atoi(strings.TrimSpace(m.Value)); err == nil {
						extruders[o.ID] = v
					}
				}
			}
		}
	}

	allMeshPaths := map[string]bool{}
	byID := map[int]*objectMeshes{}
	for _, o := range root.Objects {
		om := &objectMeshes{extruder: 1}
		if e, ok := extruders[o.ID]; ok && e > 0 {
			om.extruder = e
		}
		for _, c := range o.Components {
			zipName := normalizeMeshPath(c.Path)
			om.meshPaths = append(om.meshPaths, zipName)
			allMeshPaths[zipName] = true
		}
		for _, t := range o.InlineTriangles {
			if t.Paint != "" {
				om.inlinePaint = append(om.inlinePaint, t.Paint)
			}
		}
		byID[o.ID] = om
	}

	built := map[int]*objectMeshes{}
	for _, it := range root.Items {
		if om, ok := byID[it.ObjectID]; ok {
			built[it.ObjectID] = om
		}
	}
	return built, allMeshPaths, nil
}

func normalizeMeshPath(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(p), "/")
	return path.Clean(p)
}

// objectColorSets computes the source filament numbers each built object uses
// (base extruder plus every painted filament across its meshes).
func objectColorSets(zr *zip.Reader, built map[int]*objectMeshes) ([]objectColors, error) {
	paintCache := map[string]map[int]bool{}
	meshFilaments := func(name string) (map[int]bool, error) {
		if c, ok := paintCache[name]; ok {
			return c, nil
		}
		data, err := readZipFile(zr, name)
		if err != nil {
			return nil, err
		}
		set := map[int]bool{}
		if data != nil {
			var mesh struct {
				Triangles []struct {
					Paint string `xml:"paint_color,attr"`
				} `xml:"resources>object>mesh>triangles>triangle"`
			}
			if err := xml.Unmarshal(data, &mesh); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			for _, t := range mesh.Triangles {
				addPaintFilaments(set, t.Paint)
			}
		}
		paintCache[name] = set
		return set, nil
	}

	var out []objectColors
	for id, om := range built {
		colors := map[int]bool{om.extruder: true}
		for _, p := range om.inlinePaint {
			addPaintFilaments(colors, p)
		}
		for _, mp := range om.meshPaths {
			set, err := meshFilaments(mp)
			if err != nil {
				return nil, err
			}
			for f := range set {
				colors[f] = true
			}
		}
		out = append(out, objectColors{id: id, colors: sortedKeys(colors)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

func addPaintFilaments(set map[int]bool, paintColor string) {
	if paintColor == "" {
		return
	}
	n, err := paint.Decode(paintColor)
	if err != nil {
		return // tolerate unparseable paint rather than failing the whole split
	}
	for f := range n.Filaments() {
		set[f] = true
	}
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
