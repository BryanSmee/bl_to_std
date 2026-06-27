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

const testSliceInfo = `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <header>
    <header_item key="X-BBL-Client-Type" value="slicer"/>
  </header>
  <plate>
    <metadata key="index" value="1"/>
    <metadata key="printer_model_id" value="C13"/>
    <metadata key="nozzle_diameters" value="0.4"/>
    <object identify_id="123" name="cube" skipped="false"/>
    <filament id="1" tray_info_idx="GFA00" type="PLA" color="#FF0000" used_m="10.00" used_g="30.00"/>
    <filament id="2" tray_info_idx="GFA00" type="PLA" color="#00FF00" used_m="8.00" used_g="24.00"/>
    <filament id="3" tray_info_idx="GFA00" type="PETG" color="#0000FF" used_m="6.00" used_g="18.00"/>
    <filament id="4" tray_info_idx="GFA00" type="PLA" color="#FFFF00" used_m="4.00" used_g="12.00"/>
    <filament id="5" tray_info_idx="GFA00" type="PLA" color="#111111" used_m="2.00" used_g="6.00"/>
    <filament id="6" tray_info_idx="GFA00" type="PLA" color="#FEFEFE" used_m="1.00" used_g="3.00"/>
  </plate>
</config>
`

const testModelSettings = `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <object id="2">
    <metadata key="name" value="cube"/>
    <metadata key="extruder" value="5"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="part"/>
      <metadata key="extruder" value="6"/>
    </part>
  </object>
  <plate>
    <metadata key="plater_id" value="1"/>
  </plate>
</config>
`

// Triangles painted with filaments 2 ("8"), 5 ("2C") and a split triangle
// with children states 2,2,1 ("4882").
const testObjectModel = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021">
 <metadata name="BambuStudio:3mfVersion">1</metadata>
 <resources>
  <object id="2" type="model">
   <mesh>
    <vertices>
     <vertex x="0" y="0" z="0"/>
     <vertex x="1" y="0" z="0"/>
     <vertex x="0" y="1" z="0"/>
     <vertex x="0" y="0" z="1"/>
    </vertices>
    <triangles>
     <triangle v1="0" v2="1" v3="2" paint_color="8"/>
     <triangle v1="0" v2="1" v3="3" paint_color="2C"/>
     <triangle v1="0" v2="2" v3="3" paint_color="4882"/>
     <triangle v1="1" v2="2" v3="3"/>
    </triangles>
   </mesh>
  </object>
 </resources>
</model>
`

func testProjectSettings(t *testing.T) []byte {
	t.Helper()
	cfg := map[string]any{
		"printer_model":                "Bambu Lab A1",
		"filament_colour":              []any{"#FF0000", "#00FF00", "#0000FF", "#FFFF00", "#111111", "#FEFEFE"},
		"filament_type":                []any{"PLA", "PLA", "PETG", "PLA", "PLA", "PLA"},
		"enable_support":               "1",
		"support_type":                 "tree(auto)",
		"different_settings_to_system": []any{"enable_support;support_type", "", "", "", "", "", "", ""},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func buildFixture(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml":              `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"_rels/.rels":                      `<?xml version="1.0" encoding="UTF-8"?><Relationships/>`,
		"3D/3dmodel.model":                 `<?xml version="1.0" encoding="UTF-8"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>`,
		"3D/Objects/object_1.model":        testObjectModel,
		"Metadata/slice_info.config":       testSliceInfo,
		"Metadata/model_settings.config":   testModelSettings,
		"Metadata/project_settings.config": string(testProjectSettings(t)),
		"Metadata/plate_1.png":             "not-really-a-png",
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readOutputFile(t *testing.T, out []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	f := zipFile(zr, name)
	if f == nil {
		t.Fatalf("output missing %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInspectFixture(t *testing.T) {
	src := buildFixture(t)
	insp, err := InspectReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if len(insp.Filaments) != 6 {
		t.Fatalf("got %d filaments, want 6", len(insp.Filaments))
	}
	if insp.PrinterModelID != "C13" {
		t.Errorf("printer model = %q", insp.PrinterModelID)
	}
	if insp.Filaments[2].Type != "PETG" || insp.Filaments[2].Color != "#0000FF" {
		t.Errorf("filament 3 = %+v", insp.Filaments[2])
	}
}

func TestConvertAutoMapping(t *testing.T) {
	src := buildFixture(t)
	var out bytes.Buffer
	res, err := ConvertReader(bytes.NewReader(src), int64(len(src)), &out, Options{})
	if err != nil {
		t.Fatal(err)
	}

	checkAutoPlan(t, res)
	checkOutputProjectSettings(t, out.Bytes())
	checkOutputSliceInfo(t, out.Bytes())
	checkOutputModelSettings(t, out.Bytes())

	if got := string(readOutputFile(t, out.Bytes(), "Metadata/plate_1.png")); got != "not-really-a-png" {
		t.Errorf("raw copy corrupted: %q", got)
	}
}

func checkAutoPlan(t *testing.T, res *Result) {
	t.Helper()
	// Auto slots: the 4 most used filaments (IDs 1-4) keep their colors.
	wantSlots := []Slot{
		{Color: "#FF0000", Type: "PLA"},
		{Color: "#00FF00", Type: "PLA"},
		{Color: "#0000FF", Type: "PETG"},
		{Color: "#FFFF00", Type: "PLA"},
	}
	if len(res.Slots) != 4 {
		t.Fatalf("slots = %+v", res.Slots)
	}
	for i, want := range wantSlots {
		if res.Slots[i].Color != want.Color || res.Slots[i].Type != want.Type {
			t.Errorf("slot %d = %+v, want %+v", i+1, res.Slots[i], want)
		}
	}
	// Filaments 1-4 are the slots themselves; 5 (#111111) and 6 (#FEFEFE)
	// must be folded onto one of them by nearest color.
	for id := 1; id <= 4; id++ {
		if res.Mapping[id] != id {
			t.Errorf("mapping[%d] = %d, want identity", id, res.Mapping[id])
		}
	}
	if res.Mapping[5] == 0 || res.Mapping[6] == 0 {
		t.Errorf("mapping incomplete: %v", res.Mapping)
	}
	if !res.SupportsEnabled {
		t.Error("supports should be auto-enabled from source")
	}
}

func checkOutputProjectSettings(t *testing.T, out []byte) {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(readOutputFile(t, out, projectSettingsPath), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["printer_model"] != "Snapmaker U1" {
		t.Errorf("printer_model = %v", cfg["printer_model"])
	}
	colors := cfg["filament_colour"].([]any)
	if len(colors) != 4 || colors[0] != "#FF0000" || colors[2] != "#0000FF" {
		t.Errorf("filament_colour = %v", colors)
	}
	types := cfg["filament_type"].([]any)
	if types[2] != "PETG" {
		t.Errorf("filament_type = %v", types)
	}
	ids := cfg["filament_settings_id"].([]any)
	if ids[2] != "Snapmaker PETG HF" {
		t.Errorf("filament_settings_id = %v", ids)
	}
	if cfg["enable_support"] != "1" {
		t.Errorf("enable_support = %v", cfg["enable_support"])
	}
	if cfg["support_type"] != "tree(auto)" {
		t.Errorf("support_type = %v (should be carried from source)", cfg["support_type"])
	}
	for key, val := range cfg {
		if list, ok := val.([]any); ok && strings.HasPrefix(key, "filament_") && len(list) != 4 {
			t.Errorf("%s has %d entries, want 4", key, len(list))
		}
	}
}

func checkOutputSliceInfo(t *testing.T, out []byte) {
	t.Helper()
	si := readOutputFile(t, out, sliceInfoPath)
	var parsed sliceInfoXML
	if err := xml.Unmarshal(si, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Plates) != 1 || len(parsed.Plates[0].Filaments) != 4 {
		t.Fatalf("slice_info plates/filaments wrong: %s", si)
	}
	if !strings.Contains(string(si), `key="printer_model_id" value="Snapmaker U1"`) {
		t.Errorf("printer_model_id not retargeted: %s", si)
	}
	if !strings.Contains(string(si), `<object identify_id="123"`) {
		t.Errorf("unknown plate children were dropped: %s", si)
	}
}

func checkOutputModelSettings(t *testing.T, out []byte) {
	t.Helper()
	ms := string(readOutputFile(t, out, modelSettingsPath))
	if strings.Contains(ms, `key="extruder" value="5"`) || strings.Contains(ms, `key="extruder" value="6"`) {
		t.Errorf("extruder not remapped: %s", ms)
	}
	if !strings.Contains(ms, `key="plater_id" value="1"`) {
		t.Errorf("unrelated metadata dropped: %s", ms)
	}
}

func TestConvertExplicitSlotsRemapsPaint(t *testing.T) {
	src := buildFixture(t)
	var out bytes.Buffer
	res, err := ConvertReader(bytes.NewReader(src), int64(len(src)), &out, Options{
		Slots: []Slot{
			{Color: "#FF0000", Type: "PLA"},
			{Color: "#FFFFFF", Type: "PLA"},
		},
		Mapping:  map[int]int{2: 2, 5: 1},
		Supports: SupportsOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mapping[2] != 2 || res.Mapping[5] != 1 {
		t.Fatalf("explicit mapping not honored: %v", res.Mapping)
	}
	if res.SupportsEnabled {
		t.Error("supports should be off")
	}

	model := string(readOutputFile(t, out.Bytes(), "3D/Objects/object_1.model"))
	checkRemappedPaint(t, model)
	// Namespaced root attributes must survive the rewrite.
	if !strings.Contains(model, `xmlns:BambuStudio="http://schemas.bambulab.com/package/2021"`) {
		t.Errorf("namespace declaration lost:\n%s", model)
	}
}

func trianglePaints(t *testing.T, model string) []string {
	t.Helper()
	var parsed struct {
		Triangles []struct {
			Paint string `xml:"paint_color,attr"`
		} `xml:"resources>object>mesh>triangles>triangle"`
	}
	if err := xml.Unmarshal([]byte(model), &parsed); err != nil {
		t.Fatalf("output model is not valid XML: %v\n%s", err, model)
	}
	paints := make([]string, len(parsed.Triangles))
	for i, tr := range parsed.Triangles {
		paints[i] = tr.Paint
	}
	return paints
}

// checkRemappedPaint verifies the fixture's paint data after mapping
// filament 2 -> slot 2 and filament 5 -> slot 1.
func checkRemappedPaint(t *testing.T, model string) {
	t.Helper()
	paints := trianglePaints(t, model)
	if len(paints) != 4 {
		t.Fatalf("triangle paints = %v", paints)
	}
	// Triangle 1 was filament 2 -> slot 2 (unchanged encoding "8").
	if paints[0] != "8" {
		t.Errorf("triangle 1 paint = %q, want 8", paints[0])
	}
	// Triangle 2 was filament 5 -> slot 1 ("4").
	if paints[1] != "4" {
		t.Errorf("triangle 2 paint = %q, want 4", paints[1])
	}
	// Triangle 3 split children were filaments 2,2,1; mapping sends 1 to
	// its nearest slot (#FF0000 = slot 1), so states stay 2,2,1.
	n, err := paint.Decode(paints[2])
	if err != nil {
		t.Fatal(err)
	}
	states := []int{n.Children[0].State, n.Children[1].State, n.Children[2].State}
	if states[0] != 2 || states[1] != 2 || states[2] != 1 {
		t.Errorf("split states = %v", states)
	}
	// Unpainted triangle stays unpainted.
	if paints[3] != "" {
		t.Errorf("triangle 4 gained paint %q", paints[3])
	}
}

// ExactSlots (Spoolman mode) maps the model's colors onto a candidate pool,
// keeps only the spools actually used, and is not capped at the printer's 4
// slots.
func TestConvertExactSlotsPrunesAndExceedsFour(t *testing.T) {
	src := buildFixture(t) // 6 source filaments: red, green, blue, yellow, #111111, #FEFEFE
	var out bytes.Buffer
	candidates := []Slot{
		{Color: "#FF0000", Type: "PLA"},
		{Color: "#00FF00", Type: "PLA"},
		{Color: "#0000FF", Type: "PLA"},
		{Color: "#FFFF00", Type: "PLA"},
		{Color: "#111111", Type: "PLA"},
		{Color: "#FEFEFE", Type: "PLA"},
		{Color: "#800080", Type: "PLA"}, // extra, should be pruned
		{Color: "#00FFFF", Type: "PLA"}, // extra, should be pruned
	}
	res, err := ConvertReader(bytes.NewReader(src), int64(len(src)), &out, Options{
		Slots:      candidates,
		ExactSlots: true,
		Supports:   SupportsOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	// All 6 source colors have an exact match, so 6 distinct slots are used
	// (the 2 extras pruned) — proving both pruning and the >4 slot count.
	if len(res.Slots) != 6 {
		t.Fatalf("expected 6 used slots, got %d: %+v", len(res.Slots), res.Slots)
	}
	for src, slot := range res.Mapping {
		if slot < 1 || slot > 6 {
			t.Errorf("source %d -> slot %d out of range", src, slot)
		}
	}

	var cfg map[string]any
	if err := json.Unmarshal(readOutputFile(t, out.Bytes(), projectSettingsPath), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"filament_colour", "filament_type", "filament_settings_id"} {
		if got := len(cfg[key].([]any)); got != 6 {
			t.Errorf("%s has %d entries, want 6", key, got)
		}
	}
	// slice_info should also declare 6 filaments, not 4.
	si := readOutputFile(t, out.Bytes(), sliceInfoPath)
	var parsed sliceInfoXML
	if err := xml.Unmarshal(si, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Plates) != 1 || len(parsed.Plates[0].Filaments) != 6 {
		t.Fatalf("slice_info should have 6 filaments: %s", si)
	}
}

func TestConvertRejectsTooManySlots(t *testing.T) {
	src := buildFixture(t)
	var out bytes.Buffer
	_, err := ConvertReader(bytes.NewReader(src), int64(len(src)), &out, Options{
		Slots: make([]Slot, 5),
	})
	if err == nil || !strings.Contains(err.Error(), "4") {
		t.Fatalf("expected slot count error, got %v", err)
	}
}
