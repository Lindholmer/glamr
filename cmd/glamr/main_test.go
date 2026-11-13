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
