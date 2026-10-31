package env

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	in := `# comment
A=1
export B=two words # trailing comment
C='single # kept'
D="double \"quoted\""
E=
  F = spaced
`
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{"A", "1"},
		{"B", "two words"},
		{"C", "single # kept"},
		{"D", `double "quoted"`},
		{"E", ""},
		{"F", "spaced"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %v\nwant %v", got, want)
	}

	if _, err := Parse(strings.NewReader("NOT A VAR\n")); err == nil {
		t.Error("Parse accepted a line without =")
	}
}

func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("_shared/base.env", "A=shared-base\nB=shared-base\nC=shared-base\nD=shared-base\nE=shared-base\nF=shared-base\n")
	write("_shared/staging.env", "B=shared-staging\nC=shared-staging\nD=shared-staging\nE=shared-staging\nF=shared-staging\n")
	write("api/base.env", "C=api-base\nD=api-base\nE=api-base\nF=api-base\n")
	write("api/staging.env", "D=api-staging\nE=api-staging\nF=api-staging\n")
	write("api/me.local.env", "E=personal\nF=personal\n")
	write("api/local.env", "A=wrong-config\n")
	write("web/base.env", "A=other-repo\n")

	vars, err := Resolver{Dir: dir}.Resolve("api", "staging", map[string]string{"F": "flag"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value + " from " + v.Source
	}
	base := filepath.Base(dir)
	want := map[string]string{
		"A": "shared-base from " + base + "/_shared/base.env",
		"B": "shared-staging from " + base + "/_shared/staging.env",
		"C": "api-base from " + base + "/api/base.env",
		"D": "api-staging from " + base + "/api/staging.env",
		"E": "personal from " + base + "/api/me.local.env",
		"F": "flag from --set",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve =\n%v\nwant\n%v", got, want)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.env")
	in := []Var{
		{Key: "PLAIN", Value: "value"},
		{Key: "SPACES", Value: "two words"},
		{Key: "HASH", Value: "a #b"},
		{Key: "QUOTE", Value: `it's "here"`},
		{Key: "EMPTY", Value: ""},
	}
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	got, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range in {
		if got[i] != [2]string{v.Key, v.Value} {
			t.Errorf("round trip %s = %q, want %q", v.Key, got[i][1], v.Value)
		}
	}
}

func TestParseAssignments(t *testing.T) {
	got, err := ParseAssignments([]string{"A=1", "B=x=y"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"A": "1", "B": "x=y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if _, err := ParseAssignments([]string{"nope"}); err == nil {
		t.Error("accepted assignment without =")
	}
}

func TestConfigs(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"_shared/base.env", "_shared/local.env", "_shared/staging.env",
		"api/base.env", "api/production.env", "api/me.local.env", "web/qa.env"} {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Resolver{Dir: dir}.Configs("api", "local", "review")
	if want := []string{"local", "review", "production", "staging"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Configs = %v, want %v", got, want)
	}
}
