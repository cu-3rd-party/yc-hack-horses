package inference

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalKeys — канонический набор признаков лошади Пржевальского:
// голова / морда / уши / окрас / грива / пропорции / отметины / шрамы.
// Канонизация отбрасывает неизвестные ключи и недопустимые значения.
var CanonicalKeys = []string{
	"head",        // форма головы
	"muzzle",      // морда (носогубная зона)
	"ears",        // уши
	"coat",        // окрас
	"mane",        // грива
	"proportions", // пропорции тела
	"markings",    // отметины (проточина, чулки и т.п.)
	"scars",       // шрамы и повреждения
}

// allowedValues — допустимые значения по каждому признаку.
// Базовый набор V0; расширение — через согласование контракта.
var allowedValues = map[string][]string{
	"head":        {"straight", "convex", "large", "small"},
	"muzzle":      {"light", "dark", "white", "gray"},
	"ears":        {"erect", "tipped", "small"},
	"coat":        {"dun", "bay", "gray", "black", "chestnut"},
	"mane":        {"erect", "dark", "light", "absent"},
	"proportions": {"compact", "elongated", "stocky"},
	"markings":    {"none", "blaze", "sock", "striped", "mixed"},
	"scars":       {"none", "visible", "many"},
}

// AllowedValues возвращает допустимые значения признака (пустой слайс для неизвестного ключа).
func AllowedValues(key string) []string {
	return allowedValues[key]
}

// canonicalize приводит произвольный набор признаков к каноническому виду:
// только ключи из CanonicalKeys, значения в нижнем регистре и только из
// допустимого набора. Неизвестное — отбрасывается, не вызывает ошибку.
func canonicalize(traits map[string]string) map[string]string {
	if traits == nil {
		return nil
	}
	out := make(map[string]string, len(traits))
	for _, key := range CanonicalKeys {
		v, ok := traits[key]
		if !ok {
			continue
		}
		v = strings.ToLower(strings.TrimSpace(v))
		for _, allowed := range allowedValues[key] {
			if v == allowed {
				out[key] = v
				break
			}
		}
	}
	return out
}

// Fingerprint — sha256 канонизированного набора признаков.
// Порядок ключей и источник (map/json) не важен: одинаковый набор признаков
// даёт одинаковый fingerprint. Используется ТОЛЬКО для поиска похожих
// снимков — это НЕ биологическая идентичность (ADR-0012, ADR-0013).
func Fingerprint(traits map[string]string) string {
	canon := canonicalize(traits)
	keys := make([]string, 0, len(canon))
	for k := range canon {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s=%s", k, canon[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// MarshalCanonical — канонический JSON отсортированных признаков (для хранения).
func MarshalCanonical(traits map[string]string) (json.RawMessage, error) {
	canon := canonicalize(traits)
	b, err := json.Marshal(canon)
	if err != nil {
		return nil, fmt.Errorf("inference: marshal traits: %w", err)
	}
	return b, nil
}
