package spoolman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeSpoolman(t *testing.T, spools []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/spool" {
			http.NotFound(w, r)
			return
		}
		out := spools
		if loc := r.URL.Query().Get("location"); loc != "" {
			out = nil
			for _, s := range spools {
				if s["location"] == loc {
					out = append(out, s)
				}
			}
		}
		json.NewEncoder(w).Encode(out)
	}))
}

func spool(id int, color, material, name, loc string) map[string]any {
	return map[string]any{
		"id": id, "archived": false, "remaining_weight": 500.0, "location": loc,
		"filament": map[string]any{
			"id": id, "name": name, "material": material, "color_hex": color,
			"vendor": map[string]any{"name": "Acme"},
		},
	}
}

func TestListSpoolsOrderAndColor(t *testing.T) {
	srv := fakeSpoolman(t, []map[string]any{
		spool(3, "0000FF", "PLA", "Blue", "Shelf"),
		spool(1, "FF0000FF", "PLA Silk", "Red", "AMS"),
		spool(2, "00FF00", "PETG", "Green", "Shelf"),
	})
	defer srv.Close()
	c, _ := New(srv.URL)
	spools, err := c.ListSpools(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(spools) != 3 || spools[0].ID != 1 || spools[2].ID != 3 {
		t.Fatalf("expected spools sorted by id, got %+v", spools)
	}
	if spools[0].Color() != "#FF0000" { // 8-char hex truncated to RRGGBB
		t.Errorf("color = %q", spools[0].Color())
	}
	if spools[0].VendorName() != "Acme" {
		t.Errorf("vendor = %q", spools[0].VendorName())
	}
}

func TestListSpoolsLocationAndIDs(t *testing.T) {
	all := []map[string]any{
		spool(1, "FF0000", "PLA", "Red", "AMS"),
		spool(2, "00FF00", "PLA", "Green", "Shelf"),
		spool(3, "0000FF", "PLA", "Blue", "AMS"),
	}
	srv := fakeSpoolman(t, all)
	defer srv.Close()
	c, _ := New(srv.URL)

	byLoc, err := c.ListSpools(context.Background(), ListOptions{Location: "AMS"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byLoc) != 2 {
		t.Fatalf("location filter = %+v", byLoc)
	}

	byID, err := c.ListSpools(context.Background(), ListOptions{IDs: []int{3, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byID) != 2 || byID[0].ID != 3 || byID[1].ID != 1 {
		t.Errorf("ID selection should preserve order, got %+v", byID)
	}

	if _, err := c.ListSpools(context.Background(), ListOptions{IDs: []int{99}}); err == nil {
		t.Error("expected error for unknown spool id")
	}
}

func TestNewHostForms(t *testing.T) {
	c, err := New("192.168.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "http://192.168.1.9:7912" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
}
