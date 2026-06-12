package moonraker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakePrinter(t *testing.T, objects []string, status map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/printer/objects/list":
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"objects": objects}})
		case "/printer/objects/query":
			queried := map[string]any{}
			for key := range r.URL.Query() {
				if v, ok := status[key]; ok {
					queried[key] = v
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": queried}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestQueryFilamentsU1(t *testing.T) {
	srv := fakePrinter(t,
		[]string{"gcode_move", "extruder", "extruder1", "extruder2", "extruder3", "filament_detect"},
		map[string]any{
			"extruder":  map[string]any{"temperature": 210.3, "target": 210.0, "can_extrude": true},
			"extruder1": map[string]any{"temperature": 25.0, "target": 0.0, "can_extrude": false},
			"extruder2": map[string]any{"temperature": 25.0, "target": 0.0, "can_extrude": false},
			"extruder3": map[string]any{"temperature": 25.0, "target": 0.0, "can_extrude": false},
			"filament_detect": map[string]any{
				"info": []any{
					map[string]any{"VENDOR": "Snapmaker", "MAIN_TYPE": "PLA", "SUB_TYPE": "Basic", "RGB_1": 3368652},
					map[string]any{"VENDOR": "Generic", "MAIN_TYPE": "PETG", "SUB_TYPE": "", "RGB_1": 16711680},
					map[string]any{"MAIN_TYPE": "", "RGB_1": 0},
					map[string]any{"MAIN_TYPE": "TPU", "RGB_1": 255},
				},
			},
		})
	defer srv.Close()

	c, err := New(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := c.QueryFilaments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 4 {
		t.Fatalf("tools = %+v", tools)
	}
	if !tools[0].Detected || tools[0].Type != "PLA" || tools[0].Color != "#3366CC" || tools[0].Vendor != "Snapmaker" {
		t.Errorf("tool 0 = %+v", tools[0])
	}
	if tools[0].Temperature != 210.3 || !tools[0].CanExtrude {
		t.Errorf("tool 0 extruder state = %+v", tools[0])
	}
	if tools[1].Type != "PETG" || tools[1].Color != "#FF0000" {
		t.Errorf("tool 1 = %+v", tools[1])
	}
	// Channel 2 has no RFID data: RGB_1=0 alone must not mark it detected,
	// but black (#000000) is still reported as the color.
	if tools[2].Detected || tools[2].Type != "" {
		t.Errorf("tool 2 = %+v", tools[2])
	}
	if tools[3].Type != "TPU" || tools[3].Color != "#0000FF" {
		t.Errorf("tool 3 = %+v", tools[3])
	}
}

func TestQueryFilamentsPlainKlipper(t *testing.T) {
	srv := fakePrinter(t,
		[]string{"extruder", "extruder1"},
		map[string]any{
			"extruder":  map[string]any{"temperature": 200.0, "target": 200.0, "can_extrude": true, "filament_type": "ABS", "filament_color": "#ABCDEF"},
			"extruder1": map[string]any{"temperature": 24.0, "target": 0.0, "can_extrude": false},
		})
	defer srv.Close()

	c, err := New(strings.TrimPrefix(srv.URL, "http://"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := c.QueryFilaments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v", tools)
	}
	if !tools[0].Detected || tools[0].Type != "ABS" || tools[0].Color != "#ABCDEF" {
		t.Errorf("tool 0 = %+v", tools[0])
	}
	if tools[1].Detected {
		t.Errorf("tool 1 should be undetected: %+v", tools[1])
	}
}

func TestNewHostForms(t *testing.T) {
	cases := map[string]string{
		"192.168.1.5":           "http://192.168.1.5:7125",
		"192.168.1.5:80":        "http://192.168.1.5:80",
		"http://printer.local/": "http://printer.local",
	}
	for in, want := range cases {
		c, err := New(in, "")
		if err != nil {
			t.Fatalf("New(%q): %v", in, err)
		}
		if c.BaseURL != want {
			t.Errorf("New(%q).BaseURL = %q, want %q", in, c.BaseURL, want)
		}
	}
	if _, err := New("", ""); err == nil {
		t.Error("New(\"\") should fail")
	}
}

func TestQueryFilamentsNoExtruders(t *testing.T) {
	srv := fakePrinter(t, []string{"gcode_move"}, map[string]any{})
	defer srv.Close()
	c, _ := New(srv.URL, "")
	if _, err := c.QueryFilaments(context.Background()); err == nil {
		t.Fatal("expected error for printer without extruders")
	}
}
