// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package slots

import (
	"encoding/json"
	"testing"
)

// The serializer output is golden: the addon in game parses exactly this shape.
func TestLiteralGolden(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "nil"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"int", 42, "42"},
		{"negative int64", int64(-7), "-7"},
		{"integral float", float64(123), "123"},
		{"float", 52.6, "52.6"},
		{"small float", 0.1, "0.1"},
		{"precise float", 1.0 / 3.0, "0.3333333333333333"},
		{"big float", 2e21, "2e+21"},
		{"json number", json.Number("1764000000"), "1764000000"},
		{"string all bytes escaped", "AB", `"\065\066"`},
		{"string with quote and newline", "a\"\n", `"\097\034\010"`},
		{"utf8 string", "zażółć"[:3], `"\122\097\197"`},
		{"empty string", "", `""`},
		{"empty list", []any{}, "{}"},
		{"list", []any{1, "x", true}, `{1,"\120",true}`},
		{"table sorted keys", map[string]any{"b": 1, "a": 2}, `{["\097"]=2,["\098"]=1}`},
		{"nested", map[string]any{"t": int64(5), "reqs": []any{map[string]any{"id": int64(100), "kind": "waypoint", "x": 52.6}}, "got": []any{}},
			`{["\103\111\116"]={},["\114\101\113\115"]={{["\105\100"]=100,["\107\105\110\100"]="\119\097\121\112\111\105\110\116",["\120"]=52.6}},["\116"]=5}`},
		{"unknown type is nil", struct{}{}, "nil"},
	}
	for _, c := range cases {
		if got := Literal(c.in); got != c.want {
			t.Errorf("%s: Literal(%#v) = %s, want %s", c.name, c.in, got, c.want)
		}
	}
}

// Ids below 2^31 and all integral floats must come out as plain Lua integers.
func TestLiteralIntegersStayPlain(t *testing.T) {
	for _, v := range []any{int64(1<<31 - 1), float64(1<<31 - 1), int64(2147483640)} {
		got := Literal(v)
		if got != "2147483647" && got != "2147483640" {
			t.Errorf("Literal(%v) = %s: not a plain integer", v, got)
		}
	}
}
