package inference

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
	require.Len(t, fa, 64, "fingerprint must be sha256 hex")
	require.NotEqual(t, "", fa, "fingerprint must be sha256 hex")
	require.Equal(t, fb, fa, "same traits must give same fingerprint")
	// разный набор → другой fingerprint
	c := map[string]string{"coat": "bay"}
	require.NotEqual(t, fa, Fingerprint(c), "different traits must give different fingerprint")
	// регистр значения не важен
	require.Equal(t, Fingerprint(map[string]string{"coat": "dun"}), Fingerprint(map[string]string{"coat": "DUN"}), "value case must not change fingerprint")
}

func TestCanonicalizeDropsUnknown(t *testing.T) {
	got := canonicalize(map[string]string{
		"coat":   "DUN",
		"scars":  "visible",
		"weight": "heavy", // неизвестный ключ — отбрасывается
		"coat2":  "dun",   // неизвестный ключ — отбрасывается
	})
	want := map[string]string{"coat": "dun", "scars": "visible"}
	require.Equal(t, want, got, "canonicalize")
}

func TestCanonicalizeDropsInvalidValues(t *testing.T) {
	got := canonicalize(map[string]string{"coat": "polka-dot"})
	require.Empty(t, got, "invalid value must be dropped")
}

func TestMarshalCanonical(t *testing.T) {
	raw, err := MarshalCanonical(map[string]string{"muzzle": "light", "coat": "dun"})
	require.NoError(t, err)
	var m map[string]string
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m, 2, "unexpected canonical JSON: %s", raw)
	require.Equal(t, "dun", m["coat"], "unexpected canonical JSON: %s", raw)
	require.Equal(t, "light", m["muzzle"], "unexpected canonical JSON: %s", raw)
}

func TestCanonicalKeysComplete(t *testing.T) {
	// канонический набор из задачи: голова/морда/уши/окрас/грива/пропорции/отметины/шрамы
	want := map[string]bool{
		"head": true, "muzzle": true, "ears": true, "coat": true,
		"mane": true, "proportions": true, "markings": true, "scars": true,
	}
	require.Len(t, CanonicalKeys, len(want), "CanonicalKeys = %v", CanonicalKeys)
	for _, k := range CanonicalKeys {
		require.True(t, want[k], "unexpected canonical key %q", k)
		require.NotNil(t, allowedValues[k], "no allowed values for key %q", k)
	}
}
