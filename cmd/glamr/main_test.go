package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestStringListCollectsRepeatedFlags(t *testing.T) {
	var repos stringList
	fs := flag.NewFlagSet("glamr", flag.ContinueOnError)
	fs.Var(&repos, "repo", "")

	if err := fs.Parse([]string{"--repo", "team/a", "--repo", "team/b"}); err != nil {
		t.Fatal(err)
	}
	if want := (stringList{"team/a", "team/b"}); !reflect.DeepEqual(repos, want) {
		t.Errorf("repos = %v, want %v", repos, want)
	}
	if got := repos.String(); got != "[team/a team/b]" {
		t.Errorf("String() = %q", got)
	}
}

func TestForgeEnabled(t *testing.T) {
	tests := []struct {
		flag  bool
		host  string
		repos []string
		want  bool
	}{
		{false, "", nil, false}, // nothing set: forge not queried
		{true, "", nil, true},
		{false, "gitlab.example.com", nil, true}, // host implies the forge
		{false, "", []string{"team/app"}, true},  // repo implies the forge
	}
	for _, tt := range tests {
		if got := forgeEnabled(tt.flag, tt.host, tt.repos); got != tt.want {
			t.Errorf("forgeEnabled(%v, %q, %v) = %v, want %v", tt.flag, tt.host, tt.repos, got, tt.want)
		}
	}
}
