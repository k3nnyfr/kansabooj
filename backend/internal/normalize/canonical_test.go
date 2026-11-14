package normalize

import (
	"bytes"
	"strings"
	"testing"
)

// Task states emitted from a Python set: same contract, different order at each start
// (seen on data.gouv.fr).
func taskSpec(states, required, example string) string {
	return `{"swagger":"2.0","info":{"title":"Tasks","version":"1.0"},"paths":{"/tasks":{"get":{
"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Task"}}}}}},
"definitions":{"Task":{"type":"object","required":[` + required + `],"properties":{
"id":{"type":"string"},
"status":{"type":"string","enum":[` + states + `],"example":` + example + `},
"sample":{"type":"object","example":{"enum":["z","a"]}},
"x-meta":{"enum":["z","a"]}}}}}`
}

func TestSetOrderDoesNotMatter(t *testing.T) {
	a, err := Normalize([]byte(taskSpec(`"FAILURE","PENDING","RETRY"`, `"id","status"`, `"FAILURE"`)), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Normalize([]byte(taskSpec(`"RETRY","FAILURE","PENDING"`, `"status","id"`, `"FAILURE"`)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 != b.SHA256 {
		t.Fatalf("enum / required order changed the hash:\n%s\n---\n%s", a.Canonical, b.Canonical)
	}
	// user data is kept as written
	if !bytes.Contains(compact(a.Canonical), []byte(`"example":{"enum":["z","a"]}`)) {
		t.Errorf("an example was reordered:\n%s", a.Canonical)
	}
	// a real change of the set still counts
	c, _ := Normalize([]byte(taskSpec(`"FAILURE","PENDING"`, `"id","status"`, `"FAILURE"`)), nil)
	if c.SHA256 == a.SHA256 {
		t.Error("removing an enum value must change the hash")
	}
}

func TestExampleNoiseCanBeIgnoredAtAnyDepth(t *testing.T) {
	a, _ := Normalize([]byte(taskSpec(`"FAILURE","RETRY"`, `"id"`, `"FAILURE"`)), []string{"$..example"})
	b, _ := Normalize([]byte(taskSpec(`"RETRY","FAILURE"`, `"id"`, `"RETRY"`)), []string{"$..example"})
	if a.SHA256 != b.SHA256 {
		t.Fatalf("examples should be ignored:\n%s\n---\n%s", a.Canonical, b.Canonical)
	}
	if bytes.Contains(a.Canonical, []byte(`"example"`)) {
		t.Errorf("an example is left:\n%s", a.Canonical)
	}
}

func TestUpgradeBringsStoredFormUpToDate(t *testing.T) {
	cur, err := Normalize([]byte(taskSpec(`"PENDING","FAILURE"`, `"status","id"`, `"FAILURE"`)), nil)
	if err != nil {
		t.Fatal(err)
	}
	// what Kansabooj ≤ 0.7 stored: same document, sets in the producer's order
	legacy := bytes.Replace(cur.Canonical, []byte("\"FAILURE\",\n              \"PENDING\""), []byte("\"PENDING\",\n              \"FAILURE\""), 1)
	if bytes.Equal(legacy, cur.Canonical) {
		t.Fatalf("test setup: enum not found in\n%s", cur.Canonical)
	}
	up, sha, err := Upgrade(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if sha != cur.SHA256 || !bytes.Equal(up, cur.Canonical) {
		t.Errorf("upgraded form differs:\n%s\n---\n%s", up, cur.Canonical)
	}
	again, _, _ := Upgrade(cur.Canonical)
	if !bytes.Equal(again, cur.Canonical) {
		t.Error("Upgrade must leave an up-to-date document byte-identical")
	}
}

func compact(b []byte) []byte {
	return []byte(strings.Join(strings.Fields(string(b)), ""))
}
