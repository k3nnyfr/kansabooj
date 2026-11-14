package normalize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// unordered lists the schema keywords whose array value is a set: the order of its
// values carries no meaning, and some producers (e.g. a Python set serialized by the
// API framework) emit it in a different order at each start, which would look like a
// change. Their values are sorted.
var unordered = map[string]bool{"enum": true, "required": true}

// opaque lists the keys whose value is user data, not schema: nothing is reordered
// inside (an example containing an "enum" field must stay as written).
var opaque = map[string]bool{"example": true, "examples": true, "default": true, "const": true}

// sortSets sorts, in place, the values of unordered keywords (arrays of scalars only).
func sortSets(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if opaque[k] || strings.HasPrefix(k, "x-") {
				continue
			}
			if arr, ok := child.([]any); ok && unordered[k] && scalars(arr) {
				sortScalars(arr)
				continue
			}
			sortSets(child)
		}
	case []any:
		for _, child := range t {
			sortSets(child)
		}
	}
}

func scalars(arr []any) bool {
	for _, v := range arr {
		switch v.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

// sortScalars orders mixed-type values by their JSON encoding: total and stable.
func sortScalars(arr []any) {
	keys := make(map[any]string, len(arr))
	key := func(v any) string {
		if k, ok := keys[v]; ok {
			return k
		}
		b, _ := json.Marshal(v)
		keys[v] = string(b)
		return keys[v]
	}
	slices.SortStableFunc(arr, func(a, b any) int { return strings.Compare(key(a), key(b)) })
}

// Upgrade brings a canonical document stored by an earlier version of Kansabooj to the
// current canonical form, so that a change of normalization never shows up as a contract
// change ("phantom evolution"). It is idempotent: an up-to-date document is returned
// byte-identical. Only for OpenAPI documents (not the text of degraded mode).
func Upgrade(canonical []byte) ([]byte, string, error) {
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, "", fmt.Errorf("version stockée illisible : %w", err)
	}
	sortSets(root)
	out, err := canonicalJSON(root)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(out)
	return out, hex.EncodeToString(sum[:]), nil
}
