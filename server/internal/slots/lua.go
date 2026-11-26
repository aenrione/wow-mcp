// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition (port of wow-seer's request slots).

package slots

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// Literal renders a value as a Lua literal for a slot body. Strings are written as
// decimal byte escapes ("AB" -> "\065\066"), so no byte can ever break out of the quotes;
// integers come out plain, floats with the shortest representation that round-trips
// (strconv 'g' precision -1), and tables with sorted keys, so the output is deterministic.
// An unknown type becomes nil: the body must stay loadable whatever reaches the queue.
func Literal(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return luaString(x)
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return luaFloat(float64(x))
	case float64:
		return luaFloat(x)
	case json.Number:
		return x.String()
	case []any:
		items := make([]string, len(x))
		for i, item := range x {
			items[i] = Literal(item)
		}
		return "{" + strings.Join(items, ",") + "}"
	case map[string]any:
		return luaTable(x)
	default:
		return "nil"
	}
}

// luaString writes every byte of s as a 3-digit decimal escape.
func luaString(s string) string {
	var b strings.Builder
	b.Grow(2 + 4*len(s))
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		b.WriteByte('\\')
		c := s[i]
		b.WriteByte('0' + c/100)
		b.WriteByte('0' + c/10%10)
		b.WriteByte('0' + c%10)
	}
	b.WriteByte('"')
	return b.String()
}

// luaFloat prints an integral float as a plain integer and anything else with full precision.
// The int64 conversion is only trusted well inside its range (out of range it is undefined).
func luaFloat(f float64) string {
	if f >= -1e15 && f <= 1e15 && f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// luaTable renders a map as {["key"]=value,...} with the keys sorted.
func luaTable(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = "[" + luaString(k) + "]=" + Literal(m[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}
