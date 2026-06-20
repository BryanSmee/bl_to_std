package config

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("BL2STD_CONFIG", path)

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if (got != Config{}) {
		t.Fatalf("absent config should be empty, got %+v", got)
	}

	want := Config{Printer: "snapmaker-u1", PrinterIP: "192.168.68.64", APIKey: "secret"}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}

	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(); (got != Config{}) {
		t.Errorf("config should be empty after clear, got %+v", got)
	}
	if err := Clear(); err != nil {
		t.Errorf("clearing an absent config should not error: %v", err)
	}
}

func TestPathHonorsEnv(t *testing.T) {
	t.Setenv("BL2STD_CONFIG", "/tmp/custom/bl2std.json")
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if p != "/tmp/custom/bl2std.json" {
		t.Errorf("Path() = %q", p)
	}
}
