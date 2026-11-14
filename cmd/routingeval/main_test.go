package main

import (
	"reflect"
	"testing"
)

func TestScore(t *testing.T) {
	c := caseSpec{Expected: []string{"s.a", "s.b"}, Acceptable: []string{"s.c"}}
	for _, tc := range []struct {
		name        string
		c           caseSpec
		got         []string
		pass        bool
		missing, ex []string
	}{
		{"all expected", c, []string{"s.a", "s.b"}, true, nil, nil},
		{"acceptable is not noise", c, []string{"s.a", "s.b", "s.c"}, true, nil, nil},
		{"noise is reported but passes", c, []string{"s.a", "s.b", "s.z"}, true, nil, []string{"s.z"}},
		{"missing expected fails", c, []string{"s.a", "s.c"}, false, []string{"s.b"}, nil},
		{"empty fails a positive case", c, nil, false, []string{"s.a", "s.b"}, nil},
		{"any_of passes with one", caseSpec{AnyOf: []string{"s.x", "s.y"}}, []string{"s.y"}, true, nil, nil},
		{"any_of fails with none", caseSpec{AnyOf: []string{"s.x", "s.y"}}, []string{"s.z"}, false, []string{"s.x", "s.y"}, []string{"s.z"}},
		{"any_of alternative is not noise", caseSpec{AnyOf: []string{"s.x", "s.y"}}, []string{"s.x", "s.y"}, true, nil, nil},
		{"no_match passes on empty", caseSpec{NoMatch: true}, nil, true, nil, nil},
		{"no_match fails on any tool", caseSpec{NoMatch: true}, []string{"s.a"}, false, nil, []string{"s.a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := score(tc.c, tc.got)
			if o.Pass != tc.pass || !reflect.DeepEqual(o.Missing, tc.missing) || !reflect.DeepEqual(o.Extra, tc.ex) {
				t.Fatalf("got %+v", o)
			}
		})
	}
}
