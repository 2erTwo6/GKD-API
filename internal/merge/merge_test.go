package merge

import (
	"encoding/json"
	"testing"
)

func run(t *testing.T, dst, patch string) map[string]any {
	t.Helper()
	var d, p map[string]any
	if err := json.Unmarshal([]byte(dst), &d); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(patch), &p); err != nil {
		t.Fatal(err)
	}
	return Patch(d, p)
}

func TestPatchSetsAndOverrides(t *testing.T) {
	got := run(t, `{"model":"free","temperature":1,"messages":[]}`, `{"temperature":0.5,"max_tokens":10}`)
	out, _ := json.Marshal(got)
	want := `{"max_tokens":10,"model":"free","temperature":0.5,"messages":[]}`
	// map marshal order: keys sorted alphabetically
	if string(out) != `{"max_tokens":10,"messages":[],"model":"free","temperature":0.5}` {
		t.Fatalf("got %s", out)
	}
	_ = want
}

func TestPatchNullDeletes(t *testing.T) {
	got := run(t, `{"temperature":1,"reasoning_effort":"high"}`, `{"reasoning_effort":null}`)
	out, _ := json.Marshal(got)
	if string(out) != `{"temperature":1}` {
		t.Fatalf("got %s", out)
	}
}

func TestPatchNestedMerge(t *testing.T) {
	got := run(t, `{"a":{"x":1,"y":2},"b":3}`, `{"a":{"y":20,"z":30}}`)
	out, _ := json.Marshal(got)
	if string(out) != `{"a":{"x":1,"y":20,"z":30},"b":3}` {
		t.Fatalf("got %s", out)
	}
}

func TestPatchArrayReplaces(t *testing.T) {
	got := run(t, `{"stop":["a","b"]}`, `{"stop":["c"]}`)
	out, _ := json.Marshal(got)
	if string(out) != `{"stop":["c"]}` {
		t.Fatalf("got %s", out)
	}
}
