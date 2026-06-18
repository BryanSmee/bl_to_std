package printer

import "testing"

func TestResolveFilamentProfile(t *testing.T) {
	u1 := Builtin("snapmaker-u1")
	cases := []struct {
		vendor, mat, sub string
		want             string
	}{
		// The field report: a loaded Silk spool must pick the Silk profile,
		// not the default SnapSpeed one.
		{"Snapmaker", "PLA", "Silk", "Snapmaker PLA Silk @U1"},
		{"Snapmaker", "PLA", "SnapSpeed", "Snapmaker PLA SnapSpeed @U1"},
		{"Snapmaker", "PLA", "Basic", "Snapmaker PLA Basic @U1"},
		{"Snapmaker", "PLA", "Wood", "Snapmaker PLA Wood @U1 0.4 nozzle"},
		{"Snapmaker", "TPU", "95A HF", "Snapmaker TPU 95A HF @U1"},
		// Unknown sub-type falls back to the plain type default.
		{"Snapmaker", "PLA", "Holographic", "Snapmaker PLA SnapSpeed @U1"},
		// Unknown vendor still resolves via the Snapmaker/Generic fallbacks.
		{"AcmeFil", "PLA", "Silk", "Snapmaker PLA Silk @U1"},
		// No sub-type: the plain type default, never a Generic guess.
		{"Generic", "ABS", "", "Generic ABS"},
		{"Snapmaker", "PETG", "", "Snapmaker PETG HF"},
		{"Snapmaker", "PLA", "", "Snapmaker PLA SnapSpeed @U1"},
	}
	for _, c := range cases {
		if got := u1.ResolveFilamentProfile(c.vendor, c.mat, c.sub); got != c.want {
			t.Errorf("ResolveFilamentProfile(%q,%q,%q) = %q, want %q", c.vendor, c.mat, c.sub, got, c.want)
		}
	}
}

func TestResolveFilamentProfileNoCatalog(t *testing.T) {
	p := &Profile{
		FilamentProfiles:       map[string]string{"PLA": "Generic PLA"},
		DefaultFilamentProfile: "Generic PLA",
	}
	if got := p.ResolveFilamentProfile("Snapmaker", "PLA", "Silk"); got != "Generic PLA" {
		t.Errorf("without a catalog should use the type default, got %q", got)
	}
}
