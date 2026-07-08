package terminal

import "testing"

func TestTargetKeyStringMatchesLegacyForms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  TargetKey
		want string
	}{
		{name: "project", key: TargetKey{Kind: TargetProject, ID: "p1"}, want: "project:p1"},
		{name: "node", key: TargetKey{Kind: TargetNode, ID: "n1"}, want: "node:n1"},
		{name: "project helper", key: ProjectTarget("abc"), want: "project:abc"},
		{name: "node helper", key: NodeTarget("xyz"), want: "node:xyz"},
		{name: "empty project id", key: ProjectTarget(""), want: "project:"},
		{name: "empty node id", key: NodeTarget(""), want: "node:"},
		{name: "unknown kind renders empty", key: TargetKey{Kind: TargetKind(42), ID: "x"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.key.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseTargetKeyRoundTrip(t *testing.T) {
	t.Parallel()

	keys := []TargetKey{
		ProjectTarget("p1"),
		NodeTarget("n1"),
		ProjectTarget("id-with-dashes-01"),
		NodeTarget("node_42"),
		// IDs are opaque and may contain the delimiter; only the first prefix
		// is stripped.
		ProjectTarget("project:nested"),
		NodeTarget("node:nested"),
	}
	for _, key := range keys {
		got, err := ParseTargetKey(key.String())
		if err != nil {
			t.Fatalf("ParseTargetKey(%q) error = %v", key.String(), err)
		}
		if got != key {
			t.Fatalf("ParseTargetKey(%q) = %+v, want %+v", key.String(), got, key)
		}
	}
}

func TestParseTargetKeyAcceptsEmptyID(t *testing.T) {
	t.Parallel()

	got, err := ParseTargetKey("project:")
	if err != nil {
		t.Fatalf("ParseTargetKey(\"project:\") error = %v", err)
	}
	if got != ProjectTarget("") {
		t.Fatalf("ParseTargetKey(\"project:\") = %+v, want project with empty id", got)
	}

	got, err = ParseTargetKey("node:")
	if err != nil {
		t.Fatalf("ParseTargetKey(\"node:\") error = %v", err)
	}
	if got != NodeTarget("") {
		t.Fatalf("ParseTargetKey(\"node:\") = %+v, want node with empty id", got)
	}
}

func TestParseTargetKeyRejectsMalformed(t *testing.T) {
	t.Parallel()

	malformed := []string{
		"",
		"bogus",
		"projects",          // resource key, not a target key
		"project",           // missing delimiter
		"nodes:n1",          // wrong prefix
		"Project:p1",        // case sensitive
		" project:p1",       // leading space
		"target:project:p1", // unrecognized leading kind
		"n1",                // bare id
	}
	for _, s := range malformed {
		if got, err := ParseTargetKey(s); err == nil {
			t.Fatalf("ParseTargetKey(%q) = %+v, want error", s, got)
		}
	}
}
