package graph

import "testing"

func TestParsePackageSpecifier(t *testing.T) {
	cases := []struct{ in, pkg, sub string }{
		{"@okeep/ui/button", "@okeep/ui", "/button"},
		{"@okeep/ui", "@okeep/ui", ""},
		{"@okeep/auth-client/server", "@okeep/auth-client", "/server"},
		{"lodash", "lodash", ""},
		{"lodash/fp", "lodash", "/fp"},
		{"./tom.service", "", ""},
		{"../lib/x", "", ""},
	}
	for _, c := range cases {
		pkg, sub := ParsePackageSpecifier(c.in)
		if pkg != c.pkg || sub != c.sub {
			t.Errorf("%q → (%q,%q), want (%q,%q)", c.in, pkg, sub, c.pkg, c.sub)
		}
	}
}
