package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDependenciesIncludeInputRefs(t *testing.T) {
	n := NodeSpec{
		Name: "compose", Executor: "local.compose", Deps: []string{"gate"},
		Inputs: map[string]Input{
			"asset-ids": ListOf(From("panel-2", "asset-id"), From("panel-1", "asset-id"), Lit("")),
			"layout":    Lit("grid"),
		},
	}
	got := n.Dependencies()
	want := []string{"gate", "panel-1", "panel-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Dependencies() = %v, want %v", got, want)
	}
}

func TestLitEncodesNilSliceAsEmptyArray(t *testing.T) {
	var ids []string
	if got := string(Lit(ids).Value); got != "[]" {
		t.Fatalf("Lit(nil slice) = %s, want []", got)
	}
}

func TestInputJSONRoundTrip(t *testing.T) {
	in := ListOf(Lit("a"), From("shot-1", "asset-id"))
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var back Input
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsList || len(back.List) != 2 || back.List[1].Ref.Node != "shot-1" {
		t.Fatalf("round trip lost structure: %s", raw)
	}
}

func TestValidate(t *testing.T) {
	ok := []NodeSpec{
		{Name: "a", Executor: "x"},
		{Name: "b", Executor: "x", Inputs: map[string]Input{"p": From("a", "out")}},
		{Name: "c", Executor: "x", Deps: []string{"a", "b"}},
	}
	if err := Validate(ok, nil); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	cases := map[string][]NodeSpec{
		"duplicate": {{Name: "a", Executor: "x"}, {Name: "a", Executor: "x"}},
		"unknown":   {{Name: "a", Executor: "x", Deps: []string{"nope"}}},
		"self":      {{Name: "a", Executor: "x", Deps: []string{"a"}}},
		"cycle": {
			{Name: "a", Executor: "x", Deps: []string{"c"}},
			{Name: "b", Executor: "x", Deps: []string{"a"}},
			{Name: "c", Executor: "x", Deps: []string{"b"}},
		},
		"no executor": {{Name: "a"}},
	}
	for name, nodes := range cases {
		if err := Validate(nodes, nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestValidatePatchAgainstExisting(t *testing.T) {
	patch := []NodeSpec{{Name: "redo-1", Executor: "x", Deps: []string{"gate"}}}
	if err := Validate(patch, map[string]bool{"gate": true}); err != nil {
		t.Fatalf("patch depending on an existing node rejected: %v", err)
	}
	clash := []NodeSpec{{Name: "gate", Executor: "x"}}
	if err := Validate(clash, map[string]bool{"gate": true}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}
