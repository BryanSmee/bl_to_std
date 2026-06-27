package converter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/BryanSmee/bl_to_std/pkg/paint"
)

// meshModel returns an Objects/*.model whose triangles paint the given
// filament numbers (one painted triangle each).
func meshModel(filaments ...int) string {
	var tris strings.Builder
	for _, f := range filaments {
		pc := paint.Node{State: f}.Encode()
		tris.WriteString(`<triangle v1="0" v2="1" v3="2" paint_color="` + pc + `"/>`)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="1" type="model"><mesh><vertices>` +
		`<vertex x="0" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="0" y="1" z="0"/></vertices><triangles>` +
		tris.String() + `</triangles></mesh></object></resources></model>`
}

func splitFixture(t *testing.T, objs map[int][]int, extruders map[int]int) []byte {
	t.Helper()
	var rootObjs, buildItems, msObjs strings.Builder
	files := map[string]string{}
	var rels strings.Builder
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for id, fils := range objs {
		meshPath := "3D/Objects/obj" + itoa(id) + ".model"
		files[meshPath] = meshModel(fils...)
		rootObjs.WriteString(`<object id="` + itoa(id) + `" type="model"><components><component objectid="1" p:path="/` + meshPath + `"/></components></object>`)
		buildItems.WriteString(`<item objectid="` + itoa(id) + `" transform="1 0 0 0 1 0 0 0 1 0 0 0" printable="1"/>`)
		msObjs.WriteString(`<object id="` + itoa(id) + `"><metadata key="extruder" value="` + itoa(extruders[id]) + `"/></object>`)
	}
	for _, fils := range objs {
		_ = fils
	}
	// stable rels: one per mesh file
	var meshNames []string
	for id := range objs {
		meshNames = append(meshNames, "3D/Objects/obj"+itoa(id)+".model")
	}
	for i, m := range meshNames {
		rels.WriteString(`<Relationship Target="/` + m + `" Id="relmesh-` + itoa(i) + `" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>`)
	}
	rels.WriteString(`</Relationships>`)

	root := `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06"><resources>` +
		rootObjs.String() + `</resources><build>` + buildItems.String() + `</build></model>`

	var plate strings.Builder
	plate.WriteString(`<plate><metadata key="plater_id" value="1"/><metadata key="filament_maps" value="1 1 1 1 1 1"/>`)
	var assemble strings.Builder
	assemble.WriteString(`<assemble>`)
	for id := range objs {
		plate.WriteString(`<model_instance><metadata key="object_id" value="` + itoa(id) + `"/><metadata key="instance_id" value="0"/></model_instance>`)
		assemble.WriteString(`<assemble_item object_id="` + itoa(id) + `" instance_id="0" transform="1 0 0 0 1 0 0 0 1 0 0 0"/>`)
	}
	plate.WriteString(`</plate>`)
	assemble.WriteString(`</assemble>`)
	modelSettings := `<?xml version="1.0" encoding="UTF-8"?>
<config>` + msObjs.String() + plate.String() + assemble.String() + `</config>`

	cfg := map[string]any{
		"printer_model":   "Bambu Lab A1",
		"filament_colour": []any{"#FF0000", "#00FF00", "#0000FF", "#FFFF00", "#FF00FF", "#00FFFF"},
		"filament_type":   []any{"PLA", "PLA", "PLA", "PLA", "PLA", "PLA"},
		"enable_support":  "0",
	}
	cfgData, _ := json.Marshal(cfg)
	sliceInfo := `<?xml version="1.0" encoding="UTF-8"?>
<config><plate><metadata key="printer_model_id" value="C13"/>` +
		`<filament id="1" type="PLA" color="#FF0000" used_g="10"/><filament id="2" type="PLA" color="#00FF00" used_g="10"/>` +
		`<filament id="3" type="PLA" color="#0000FF" used_g="10"/><filament id="4" type="PLA" color="#FFFF00" used_g="10"/>` +
		`<filament id="5" type="PLA" color="#FF00FF" used_g="10"/><filament id="6" type="PLA" color="#00FFFF" used_g="10"/></plate></config>`

	files["[Content_Types].xml"] = `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`
	files["_rels/.rels"] = `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel-1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`
	files[rootModelPath] = root
	files[rootModelRelsPath] = rels.String()
	files[modelSettingsPath] = modelSettings
	files[projectSettingsPath] = string(cfgData)
	files[sliceInfoPath] = sliceInfo

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

func itoa(i int) string { return string(rune('0' + i)) } // single-digit ids only in tests

func TestSplitGroupsByColor(t *testing.T) {
	// obj2 -> {1,2}, obj3 -> {3,4}, obj4 -> {1,5,6}; max 4.
	src := splitFixture(t,
		map[int][]int{2: {1, 2}, 3: {3, 4}, 4: {1, 5, 6}},
		map[int]int{2: 1, 3: 3, 4: 1})
	res, err := SplitReader(bytes.NewReader(src), int64(len(src)), "model", SplitOptions{Supports: SupportsOff})
	if err != nil {
		t.Fatal(err)
	}
	// obj4 (3 colors) packs with obj2 -> {1,2,5,6} (4); obj3 -> own batch.
	if len(res.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(res.Files), res.Files)
	}
	for _, f := range res.Files {
		if len(f.Filaments) > 4 {
			t.Errorf("file %s has %d colors", f.Name, len(f.Filaments))
		}
	}
	assertBatchValid(t, res.Files[0])
	assertBatchValid(t, res.Files[1])
}

// assertBatchValid checks one output file: it is a zip with renumbered
// filaments (1..k), only its objects in the build, and only its meshes.
func assertBatchValid(t *testing.T, f SplitFile) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(f.Data), int64(len(f.Data)))
	if err != nil {
		t.Fatalf("%s not a zip: %v", f.Name, err)
	}
	get := func(name string) []byte {
		zf := zipFile(zr, name)
		if zf == nil {
			return nil
		}
		rc, _ := zf.Open()
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return b
	}

	var cfg map[string]any
	json.Unmarshal(get(projectSettingsPath), &cfg)
	if got := len(cfg["filament_colour"].([]any)); got != len(f.Filaments) {
		t.Errorf("%s: filament_colour has %d entries, want %d", f.Name, got, len(f.Filaments))
	}

	// build items only reference this batch's objects
	var root struct {
		Items []struct {
			ObjectID int `xml:"objectid,attr"`
		} `xml:"build>item"`
	}
	xml.Unmarshal(get(rootModelPath), &root)
	want := map[int]bool{}
	for _, id := range f.ObjectIDs {
		want[id] = true
	}
	if len(root.Items) != len(f.ObjectIDs) {
		t.Errorf("%s: build has %d items, want %d", f.Name, len(root.Items), len(f.ObjectIDs))
	}
	for _, it := range root.Items {
		if !want[it.ObjectID] {
			t.Errorf("%s: build references non-batch object %d", f.Name, it.ObjectID)
		}
	}

	// only this batch's mesh files are present
	for id := 2; id <= 9; id++ {
		name := "3D/Objects/obj" + itoa(id) + ".model"
		present := zipFile(zr, name) != nil
		if present != want[id] {
			t.Errorf("%s: mesh %s present=%v, want %v", f.Name, name, present, want[id])
		}
	}
}

func TestSplitErrorsOnOver4ColorObject(t *testing.T) {
	src := splitFixture(t,
		map[int][]int{2: {1, 2, 3, 4, 5}}, // one object, 5 colors
		map[int]int{2: 1})
	_, err := SplitReader(bytes.NewReader(src), int64(len(src)), "model", SplitOptions{Supports: SupportsOff})
	if err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("expected over-4-color error, got %v", err)
	}
}
