// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"sort"

	"github.com/expr-lang/expr/builtin"
)

type catFunction struct {
	Name        string `json:"name"`
	Signature   string `json:"signature"`
	Description string `json:"description"`
	Example     string `json:"example,omitempty"`
	Gearbox     bool   `json:"gearbox"`
}

type catOperator struct {
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
}

type catCondOp struct {
	Op          string `json:"op"`
	Description string `json:"description"`
	ValueKind   string `json:"valueKind"`
}

type catVariable struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

var gearboxFunctionDocs = []catFunction{
	{"lookup", "lookup(table string, key any[, default any]) any", "Value from a bundle lookup table; default (or nil) when the key is missing.", `lookup("city_zone", input.lead.city, "other")`, true},
	{"inSegment", "inSegment(segment string, key any) bool", "Membership in a static or rollout segment (key), or a dynamic segment (map of attributes).", `inSegment("vip_dealers", input.dealer.id)`, true},
	{"gate", "gate(name string) bool", "Gate result for ctx.unit; the node input is passed as attributes.", `gate("new_pricing")`, true},
	{"variant", "variant(experiment string) string", "Assigned variant for ctx.unit (the default variant when not in the experiment); records an exposure.", `variant("checkout_v2") == "treatment"`, true},
	{"bucket", "bucket(salt string) int", "Stable bucket 0-9999 for ctx.unit; nil without a unit, so comparisons are false.", `bucket("r1-2026-10") < 2000`, true},
	{"daysBetween", "daysBetween(a time|string, b time|string) int", "Whole days from a to b (floor); nil when either is nil.", `daysBetween(date(input.lead.created_at), ctx.now) > 30`, true},
	{"coalesce", "coalesce(a, b, ...) any", "First argument that is not nil.", `coalesce(input.lead.alt_phone, input.lead.city)`, true},
	{"keys", "keys(map) []string", "Map keys sorted (deterministic; replaces the Expr builtin).", `keys(input.flags)`, true},
	{"values", "values(map) []any", "Map values ordered by sorted key (replaces the Expr builtin).", `values(input.flags)`, true},
	{"toPairs", "toPairs(map) [][key, value]", "Pairs ordered by key (replaces the Expr builtin).", `toPairs(input.flags)`, true},
}

var builtinDocs = map[string]string{
	"len": "Length of a string, list or map.", "type": "Type name of a value.", "abs": "Absolute value.",
	"ceil": "Round up.", "floor": "Round down.", "round": "Round to nearest.", "int": "Convert to integer.",
	"float": "Convert to float.", "string": "Convert to string.", "max": "Largest argument or list element.",
	"min": "Smallest argument or list element.", "mean": "Average of a list.", "median": "Median of a list.",
	"sum": "Sum of a list (optionally of a predicate).", "count": "Elements matching a predicate.",
	"trim": "Trim whitespace or characters.", "trimPrefix": "Remove a prefix.", "trimSuffix": "Remove a suffix.",
	"upper": "Upper case.", "lower": "Lower case.", "split": "Split a string.", "splitAfter": "Split after separators.",
	"replace": "Replace substrings.", "repeat": "Repeat a string.", "join": "Join a list of strings.",
	"indexOf": "Index of a substring.", "lastIndexOf": "Last index of a substring.", "hasPrefix": "String starts with.",
	"hasSuffix": "String ends with.", "all": "Predicate true for all elements.", "any": "Predicate true for any element.",
	"one": "Predicate true for exactly one element.", "none": "Predicate true for no element.", "map": "Transform each element.",
	"filter": "Keep matching elements.", "find": "First matching element.", "findIndex": "Index of first match.",
	"findLast": "Last matching element.", "findLastIndex": "Index of last match.", "groupBy": "Group elements by a key.",
	"sortBy": "Sort by a key.", "sort": "Sort a list.", "reduce": "Fold a list.", "first": "First element.",
	"last": "Last element.", "take": "First n elements.", "reverse": "Reverse a list.", "uniq": "Remove duplicates.",
	"concat": "Concatenate lists.", "flatten": "Flatten nested lists.", "fromPairs": "Map from pairs.",
	"get": "Element or field with nil on missing.", "toJSON": "Encode as JSON (sorted keys).", "fromJSON": "Decode JSON.",
	"toBase64": "Base64 encode.", "fromBase64": "Base64 decode.", "duration": "Parse a duration (\"1h30m\").",
	"date": "Parse a date/time (UTC unless the string names a zone).", "timezone": "Load a time zone.",
	"bitand": "Bitwise and.", "bitor": "Bitwise or.", "bitxor": "Bitwise xor.", "bitnand": "Bitwise and-not.",
	"bitnot": "Bitwise not.", "bitshl": "Shift left.", "bitshr": "Shift right.", "bitushr": "Unsigned shift right.",
}

var condOpDocs = map[string]string{
	"EQ": "Equal (numbers compare numerically).", "NEQ": "Not equal.",
	"EQ_CASE_INSENSITIVE": "String equality ignoring case.", "NEQ_CASE_INSENSITIVE": "String inequality ignoring case.",
	"GT": "Greater than (numeric strings are parsed).", "GTE": "Greater than or equal.", "LT": "Less than.", "LTE": "Less than or equal.",
	"BETWEEN": "value <= x <= valueTo (inclusive).", "IN": "One of the list.", "NOT_IN": "Not one of the list.",
	"IN_CASE_INSENSITIVE": "One of the list, ignoring case.", "NOT_IN_CASE_INSENSITIVE": "Not one of the list, ignoring case.",
	"IN_NUMERIC": "Numerically one of the list.", "NOT_IN_NUMERIC": "Numerically not one of the list.",
	"CONTAINS": "Substring of a string, or element of a list.", "NOT_CONTAINS": "Neither substring nor element.",
	"CONTAINS_CASE_INSENSITIVE": "Contains, ignoring case.", "NOT_CONTAINS_CASE_INSENSITIVE": "Does not contain, ignoring case.",
	"CONTAINS_NUMERIC": "List contains the number.", "NOT_CONTAINS_NUMERIC": "List does not contain the number.",
	"CONTAINS_ANY": "List shares an element with the value list.", "CONTAINS_NONE": "List shares no element with the value list.",
	"STARTS_WITH": "String prefix.", "ENDS_WITH": "String suffix.", "STARTS_WITH_CASE_INSENSITIVE": "Prefix ignoring case.",
	"ENDS_WITH_CASE_INSENSITIVE": "Suffix ignoring case.", "MATCHES": "RE2 regular expression matches.",
	"NOT_MATCHES": "RE2 regular expression does not match.", "MATCHES_CASE_INSENSITIVE": "Regex matches ignoring case.",
	"NOT_MATCHES_CASE_INSENSITIVE": "Regex does not match ignoring case.", "EXISTS": "Field present and not null.",
	"NOT_EXISTS": "Field missing or null (the only operator true for a missing field).",
	"IS_TRUE":    "true, or the string \"true\".", "IS_FALSE": "false, or the string \"false\".",
}

var catalogueJSON = buildCatalogue()

func buildCatalogue() json.RawMessage {
	var fns []catFunction
	fns = append(fns, gearboxFunctionDocs...)
	var names []string
	for name := range builtin.Index {
		if name == "now" || gearboxFuncs[name] || builtinDocs[name] == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, n := range names {
		fns = append(fns, catFunction{Name: n, Signature: n + "(...)", Description: builtinDocs[n]})
	}
	ops := []catOperator{
		{"+", "Add numbers or concatenate strings and lists."}, {"-", "Subtract."}, {"*", "Multiply."}, {"/", "Divide (float)."},
		{"%", "Modulo."}, {"^", "Power (also **)."}, {"==", "Equal (nil == nil is true)."}, {"!=", "Not equal."},
		{"<", "Less than; false when either side is nil."}, {"<=", "Less or equal; false with nil."}, {">", "Greater; false with nil."},
		{">=", "Greater or equal; false with nil."}, {"and", "Logical and (&&)."}, {"or", "Logical or (||)."}, {"not", "Logical not (!)."},
		{"in", "Membership in a list or map keys."}, {"contains", "String contains."}, {"startsWith", "String prefix."},
		{"endsWith", "String suffix."}, {"matches", "RE2 regex match."}, {"??", "Nil coalescing."}, {"?:", "Ternary."},
		{"..", "Integer range."}, {"|", "Pipe into a function."}, {"?.", "Optional member access (every access is already nil-safe)."},
		{"let", "Local variable: let x = 1; x + 1."},
	}
	var conds []catCondOp
	kindName := map[opKind]string{vkNone: "none", vkScalar: "scalar", vkNumber: "scalar", vkString: "scalar", vkList: "list", vkRange: "range", vkRegex: "regex"}
	for _, op := range fieldOps {
		conds = append(conds, catCondOp{Op: op, Description: condOpDocs[op], ValueKind: kindName[opValueKind[op]]})
	}
	conds = append(conds,
		catCondOp{"IN_SEGMENT", "Member of a segment (static: key; dynamic: attributes; rollout: key or ctx.unit).", "segment"},
		catCondOp{"NOT_IN_SEGMENT", "Not a member of the segment.", "segment"},
		catCondOp{"GATE_PASSES", "Gate is on for this unit.", "gate"},
		catCondOp{"GATE_FAILS", "Gate is off for this unit.", "gate"},
	)
	vars := []catVariable{
		{"input", "any", "The node's input (validated against the node's inputSchema for rules and table nodes)."},
		{"request", "any", "The original request input."},
		{"nodes", "map", "Outputs of nodes that already ran, by node ID: nodes.eligibility.lane."},
		{"out", "map", "Transform nodes only: fields assigned so far."},
		{"value", "any", "Decision table cells only: the column value."},
		{"ctx.now", "time", "Evaluation time (DecideOptions.now or the sidecar clock); now() is disabled."},
		{"ctx.unit", "string", "unit_id of the request (empty when not sent)."},
		{"ctx.requestId", "string", "Request ID."},
		{"ctx.env", "string", "Environment (dev, stage, prod)."},
	}
	b, _ := json.Marshal(map[string]any{"functions": fns, "operators": ops, "conditionOperators": conds, "variables": vars})
	return b
}
