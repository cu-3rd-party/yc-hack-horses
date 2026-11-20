package inference

import (
	"encoding/json"
	"testing"
)

// Канонизация/fingerprint: одинаковый набор признаков → одинаковый fingerprint,
// порядок ключей и лишние/недопустимые ключи не важны.
func TestFingerprintDeterministic(t *testing.T) {
	a := map[string]string{
		"coat":        "dun",
		"muzzle":      "light",
		"markings":    "none",
		"proportions": "compact",
	}
	b := map[string]string{
		"proportions": "compact",
		"markings":    "none",
		"muzzle":      "light",
		"coat":        "dun",
		"unknown_key": "ignored",
	}
	fa, fb := Fingerprint(a), Fingerprint(b)
	if fa == "" || len(fa) != 64 {
		t.Fatalf("fingerprint must be sha256 hex, got %q", fa)
	}
	if fa != fb {
		t.Fatalf("same traits must give same fingerprint: %s != %s", fa, fb)
	}
	// разный набор → другой fingerprint
	c := map[string]string{"coat": "bay"}
	if Fingerprint(c) == fa {
		t.Fatal("different traits must give different fingerprint")
	}
	// регистр значения не важен
	if Fingerprint(map[string]string{"coat": "DUN"}) != Fingerprint(map[string]string{"coat": "dun"}) {
		t.Fatal("value case must not change fingerprint")
	}
}

func TestCanonicalizeDropsUnknown(t *testing.T) {
	got := canonicalize(map[string]string{
		"coat":   "DUN",
		"scars":  "visible",
		"weight": "heavy", // неизвестный ключ — отбрасывается
		"coat2":  "dun",   // неизвестный ключ — отбрасывается
	})
	want := map[string]string{"coat": "dun", "scars": "visible"}
	if len(got) != len(want) {
		t.Fatalf("canonicalize: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("canonicalize[%q]: got %q, want %q", k, got[k], v)
		}
	}
}

func TestCanonicalizeDropsInvalidValues(t *testing.T) {
	got := canonicalize(map[string]string{"coat": "polka-dot"})
	if len(got) != 0 {
		t.Fatalf("invalid value must be dropped, got %v", got)
	}
}

func TestMarshalCanonical(t *testing.T) {
	raw, err := MarshalCanonical(map[string]string{"muzzle": "light", "coat": "dun"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["coat"] != "dun" || m["muzzle"] != "light" {
		t.Fatalf("unexpected canonical JSON: %s", raw)
	}
}

func TestCanonicalKeysComplete(t *testing.T) {
	// канонический набор из задачи: голова/морда/уши/окрас/грива/пропорции/отметины/шрамы
	want := map[string]bool{
		"head": true, "muzzle": true, "ears": true, "coat": true,
		"mane": true, "proportions": true, "markings": true, "scars": true,
	}
	if len(CanonicalKeys) != len(want) {
		t.Fatalf("CanonicalKeys = %v, want 8 keys", CanonicalKeys)
	}
	for _, k := range CanonicalKeys {
		if !want[k] {
			t.Fatalf("unexpected canonical key %q", k)
		}
		if allowedValues[k] == nil {
			t.Fatalf("no allowed values for key %q", k)
		}
	}
}
