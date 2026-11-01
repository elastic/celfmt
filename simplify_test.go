// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package celfmt

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/ext"

	"github.com/elastic/mito/lib"
)

func TestSimplify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		opts []FormatOption
	}{
		// .as() inlining
		{name: "as_single_use", in: `x.as(v, v + 1)`, want: `x + 1`},
		{name: "as_multi_use_unchanged", in: `x.as(v, v + v)`, want: `x.as(v, v + v)`},
		{name: "as_zero_use", in: `x.as(v, 42)`, want: `42`},
		{name: "as_nested_both_single", in: `a.as(x, b.as(y, x + y))`, want: `a + b`},
		{name: "as_nested_inner_multi", in: `a.as(x, b.as(y, y + y + x))`, want: `b.as(y, y + y + a)`},
		{name: "as_chain", in: `x.as(v, v.size())`, want: `x.size()`},
		// An inlined .as() in a branch of a conditional in an .as() that stays.
		{name: "as_in_conditional_branch", in: `x.as(r, r.b ? r.c.as(v, {"k": v.d}) : {})`, want: `x.as(r, r.b ? {"k": r.c.d} : {})`},
		{name: "as_in_conditional_branch_filter_map", in: `x.as(r, r.b ? r.i.filter(v, v > 0).map(v, v * 2).as(q, {"k": q}) : r.i)`, want: `x.as(r, r.b ? {"k": r.i.map(v, v > 0, v * 2)} : r.i)`},
		{name: "as_inner_inlined_outer_multi_use", in: `a.as(v, (v + v).as(w, w + 1))`, want: `a.as(v, v + v + 1)`},
		{name: "as_init_filter_map", in: `x.filter(v, v > 0).map(v, v * 2).as(w, w)`, want: `x.map(v, v > 0, v * 2)`},
		// The initial value uses the name that is bound.
		{name: "as_self_reference", in: `x.size().as(x, x + 1)`, want: `x.size() + 1`},
		{name: "as_rebound_unused_outer", in: `x.c.as(c, x.drop("c").as(x, x.map(k, k)))`, want: `x.drop("c").map(k, k)`},
		{name: "as_rebound_used_outer", in: `x.c.as(c, x.drop("c").as(x, c.map(k, k)))`, want: `x.c.map(k, k)`},

		// The initial value calls a function that is not pure.
		{name: "as_impure_zero_use", in: `get("http://example.com/x").as(r, {"a": 1})`, want: `get("http://example.com/x").as(r, {"a": 1})`},
		{name: "as_impure_single_use", in: `now().as(t, {"a": t})`, want: `now().as(t, {"a": t})`},
		{name: "as_impure_in_init", in: `x.map(v, get(v)).as(r, r.size())`, want: `x.map(v, get(v)).as(r, r.size())`},
		{name: "as_impure_uuid", in: `uuid().as(u, {"a": u})`, want: `uuid().as(u, {"a": u})`},
		{name: "as_impure_in_result", in: `x.as(r, {"a": r, "t": now()})`, want: `{"a": x, "t": now()}`},
		{name: "as_impure_inner_kept_outer_inlined", in: `x.as(r, now().as(t, [r, t]))`, want: `now().as(t, [x, t])`},

		// Boolean comparison elimination
		{name: "eq_true", in: `x == true`, want: `x`},
		{name: "eq_false", in: `x == false`, want: `!x`},
		{name: "true_eq_x", in: `true == x`, want: `x`},
		{name: "false_eq_x", in: `false == x`, want: `!x`},

		// No change when not applicable
		{name: "eq_non_bool", in: `x == 1`, want: `x == 1`},
		{name: "neq_unchanged", in: `x != true`, want: `x != true`},

		// has(x.f) ? x.f : d → x.?f.orValue(d)
		{name: "has_ternary", in: `has(x.f) ? x.f : "default"`, want: `x.?f.orValue("default")`},
		{name: "has_ternary_in_as_init", in: `(has(x.f) ? x.f : 0).as(v, v + v)`, want: `x.?f.orValue(0).as(v, v + v)`},
		{name: "has_ternary_in_map_body", in: `x.map(v, has(v.f) ? v.f : 0)`, want: `x.map(v, v.?f.orValue(0))`},
		{name: "has_ternary_deep", in: `has(x.f.g) ? x.f.g : 0`, want: `x.f.?g.orValue(0)`},
		// The rewrite leaves the variable used once, so it is inlined in the same pass.
		{name: "has_ternary_then_as", in: `x.as(b, {"w": has(b.s) ? b.s : []})`, want: `{"w": x.?s.orValue([])}`},
		{name: "has_ternary_negated", in: `!has(x.f) ? "default" : x.f`, want: `x.?f.orValue("default")`},
		{name: "has_ternary_no_match_op", in: `has(x.f) ? x.f + 1 : 0`, want: `has(x.f) ? (x.f + 1) : 0`},
		{name: "has_ternary_no_match_field", in: `has(x.f) ? x.g : 0`, want: `has(x.f) ? x.g : 0`},
		{name: "has_ternary_no_match_operand", in: `has(x.f) ? y.f : 0`, want: `has(x.f) ? y.f : 0`},
		// has ternary in the arguments of a macro call
		{name: "has_ternary_in_macro_arg", in: `x.map(e, has(e.f) ? e.f : "d")`, want: `x.map(e, e.?f.orValue("d"))`},
		{name: "has_ternary_negated_in_macro_arg", in: `x.map(e, !has(e.f) ? "d" : e.f)`, want: `x.map(e, e.?f.orValue("d"))`},
		{name: "has_ternary_nested_macro_arg", in: `x.map(e, has(e.f.g) ? e.f.g : e.h.map(k, has(k.z) ? k.z : 0))`, want: `x.map(e, e.f.?g.orValue(e.h.map(k, k.?z.orValue(0))))`},
		{name: "has_ternary_no_match_in_macro_arg", in: `x.map(e, has(e.f) ? e.g : "d")`, want: `x.map(e, has(e.f) ? e.g : "d")`},
		{name: "has_ternary_in_as", in: `x.as(r, has(r.f) ? r.f : r.g)`, want: `x.as(r, r.?f.orValue(r.g))`},
		{name: "has_ternary_in_inlined_macro", in: `x.map(e, has(e.f) ? e.f : "d").as(q, {"k": q})`, want: `{"k": x.map(e, e.?f.orValue("d"))}`},
		{
			name: "has_ternary_comment_on_access",
			in:   "has(x.f) ?\n\t// keep this\n\tx.f\n:\n\t0",
			want: "has(x.f) ?\n\t// keep this\n\tx.f\n:\n\t0",
			opts: []FormatOption{Pretty()},
		},

		// fold filter into map
		{name: "filter_map", in: `x.filter(v, v > 0).map(v, v * 2)`, want: `x.map(v, v > 0, v * 2)`},
		{name: "filter_map_rename", in: `x.filter(e, e > 0).map(f, f * 2)`, want: `x.map(f, f > 0, f * 2)`},
		{name: "filter_map_rename_capture", in: `x.filter(e, y.exists(f, f == e)).map(f, f * 2)`, want: `x.filter(e, y.exists(f, f == e)).map(f, f * 2)`},
		{name: "filter_map_already_filtered", in: `x.map(v, v > 0, v * 2)`, want: `x.map(v, v > 0, v * 2)`},
		{name: "filter_map_chained", in: `x.filter(v, v > 0).filter(v, v < 10).map(v, v * 2)`, want: `x.filter(v, v > 0).map(v, v < 10, v * 2)`},
		{name: "filter_map_macro_source", in: `x.map(v, v + 1).filter(v, v > 0).map(v, v * 2)`, want: `x.map(v, v + 1).map(v, v > 0, v * 2)`},
		// transform* not folded (unsafe for map sources)
		{name: "filter_transformList_unchanged", in: `x.filter(v, v > 0).transformList(_, v, v * 2)`, want: `x.filter(v, v > 0).transformList(_, v, v * 2)`},
	}

	env := newTestEnv(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, iss := env.Compile(tt.in)
			if iss != nil {
				t.Fatalf("Compile(%q): %v", tt.in, iss)
			}
			native := compiled.NativeRep()
			src := common.NewTextSource(tt.in)
			Simplify(native, src)
			var buf strings.Builder
			err := Format(&buf, native, src, tt.opts...)
			if err != nil {
				t.Fatalf("Format() after Simplify(%q): %v", tt.in, err)
			}
			got := buf.String()
			if got != tt.want {
				t.Errorf("Simplify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// simplifyOnce compiles in, simplifies it and returns it formatted.
func simplifyOnce(t *testing.T, env *cel.Env, in string) string {
	t.Helper()
	compiled, iss := env.Compile(in)
	if iss != nil {
		t.Fatalf("Compile(%q): %v", in, iss)
	}
	native := compiled.NativeRep()
	src := common.NewTextSource(in)
	Simplify(native, src)
	var buf strings.Builder
	err := Format(&buf, native, src, Pretty())
	if err != nil {
		t.Fatalf("Format() after Simplify(%q): %v", in, err)
	}
	return buf.String()
}

// TestSimplifyRepeated checks results that depend on the order in which the
// macro calls are visited, which the map of them does not fix, by running
// each case many times.
func TestSimplifyRepeated(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The macro call of the filter is registered under two IDs once
		// the .as() has been inlined, and both have to be folded.
		{name: "as_init_filter_map", in: `x.filter(v, v > 0).map(v, v * 2).as(w, w)`, want: `x.map(v, v > 0, v * 2)`},
		// Inlining c first would put the outer x into the scope of the
		// inner x.
		{name: "rebound_name", in: `x.c.as(c, x.drop("c").as(x, c.map(k, {"m": x.with({"k": k})})))`, want: `x.c.map(k, {"m": x.drop("c").with({"k": k})})`},
	}

	env := newTestEnv(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 500; i++ {
				got := simplifyOnce(t, env, tt.in)
				if got != tt.want {
					t.Fatalf("Simplify(%q), run %d = %q, want %q", tt.in, i+1, got, tt.want)
				}
			}
		})
	}
}

// TestSimplifyLayout checks that the layout of a macro call that Simplify
// inlines the initial value of is that of the source, and does not depend on
// the order in which the map of macro calls is iterated.
func TestSimplifyLayout(t *testing.T) {
	// Inlining the first .as() leaves its macro call registered under both
	// the original and the substituted ID. The substituted ID is on a later
	// line, so using it would put the first argument on a line of its own.
	const in = "x.?cursor.next_link.orValue(\"\").as(next_link,\n" +
		"\t(next_link != \"\") ?\n" +
		"\t\tnext_link\n" +
		"\t:\n" +
		"\t\t\"z\"\n" +
		").as(out,\n" +
		"\ty.as(w,\n" +
		"\t\tw + out\n" +
		"\t)\n" +
		")"
	const want = "y + x.?cursor.next_link.orValue(\"\").as(next_link,\n" +
		"\t(next_link != \"\") ?\n" +
		"\t\tnext_link\n" +
		"\t:\n" +
		"\t\t\"z\"\n" +
		")"

	env := newTestEnv(t)
	// The unordered iteration chose differently about one time in eight.
	for i := 0; i < 500; i++ {
		got := simplifyOnce(t, env, in)
		if got != want {
			t.Fatalf("Simplify(%q), run %d:\n got: %q\nwant: %q", in, i+1, got, want)
		}
	}
}

func newTestEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(
		cel.VariableDecls(
			decls.NewVariable("x", types.DynType),
			decls.NewVariable("y", types.DynType),
			decls.NewVariable("a", types.DynType),
			decls.NewVariable("b", types.DynType),
		),
		lib.Collections(),
		lib.HTTP(nil, nil, nil),
		lib.Time(),
		lib.Crypto(),
		cel.OptionalTypes(cel.OptionalTypesVersion(lib.OptionalTypesVersion)),
		ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(lib.OptionalTypesVersion)),
		cel.EnableMacroCallTracking(),
	)
	if err != nil {
		t.Fatalf("cel.NewEnv: %v", err)
	}
	return env
}

// pure is the set of the functions of the mito library that are known to be
// pure, which is every function that is not in impure.
var pure = map[string]bool{
	"base64":                            true,
	"base64_decode":                     true,
	"base64_raw":                        true,
	"base64_raw_decode":                 true,
	"basic_authentication":              true,
	"canonical_mime_header_key":         true,
	"collate":                           true,
	"compare":                           true,
	"contains_any":                      true,
	"contains_substr":                   true,
	"count":                             true,
	"decode_json":                       true,
	"decode_json_stream":                true,
	"decode_json_stream_string_numbers": true,
	"decode_json_string_numbers":        true,
	"decode_xml":                        true,
	"drop":                              true,
	"drop_empty":                        true,
	"encode_json":                       true,
	"equal_fold":                        true,
	"fields":                            true,
	"flatten":                           true,
	"format":                            true,
	"format_query":                      true,
	"format_url":                        true,
	"front":                             true,
	"get_request":                       true,
	"has_prefix":                        true,
	"has_suffix":                        true,
	"hex":                               true,
	"hex_decode":                        true,
	"hmac":                              true,
	"index":                             true,
	"index_any":                         true,
	"is_error":                          true,
	"join":                              true,
	"keys":                              true,
	"last_index":                        true,
	"last_index_any":                    true,
	"max":                               true,
	"md5":                               true,
	"mime":                              true,
	"min":                               true,
	"parse_query":                       true,
	"parse_time":                        true,
	"parse_url":                         true,
	"post_request":                      true,
	"re_find":                           true,
	"re_find_all":                       true,
	"re_find_all_submatch":              true,
	"re_find_submatch":                  true,
	"re_match":                          true,
	"re_replace_all":                    true,
	"repeat":                            true,
	"replace":                           true,
	"replace_all":                       true,
	"request":                           true,
	"round":                             true,
	"sha1":                              true,
	"sha256":                            true,
	"split":                             true,
	"split_after":                       true,
	"split_after_n":                     true,
	"split_n":                           true,
	"sprintf":                           true,
	"substring":                         true,
	"sum":                               true,
	"tail":                              true,
	"to_lower":                          true,
	"to_title":                          true,
	"to_upper":                          true,
	"to_valid_utf8":                     true,
	"trim":                              true,
	"trim_left":                         true,
	"trim_prefix":                       true,
	"trim_right":                        true,
	"trim_space":                        true,
	"trim_suffix":                       true,
	"truncate":                          true,
	"try":                               true,
	"valid_utf8":                        true,
	"values":                            true,
	"with":                              true,
	"with_replace":                      true,
	"with_update":                       true,
	"zip":                               true,
}

// TestImpureClassified checks that every function that the mito library adds
// to the environment is in either impure or pure, so that a function that is
// added to the library has to be classified, and that the two do not overlap
// or name a function that is not there.
func TestImpureClassified(t *testing.T) {
	xml, err := lib.XML(nil, nil)
	if err != nil {
		t.Fatalf("lib.XML: %v", err)
	}
	opts := []cel.EnvOption{
		cel.OptionalTypes(cel.OptionalTypesVersion(lib.OptionalTypesVersion)),
		ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(lib.OptionalTypesVersion)),
	}
	base, err := cel.NewEnv(opts...)
	if err != nil {
		t.Fatalf("cel.NewEnv: %v", err)
	}
	all, err := cel.NewEnv(append(opts,
		lib.AWS(), lib.Collections(), lib.CSV(), lib.Crypto(), lib.Debug(nil), lib.Emit(nil),
		lib.File(nil), lib.HTTP(&http.Client{}, nil, nil), lib.JSON(nil), lib.Limit(nil),
		lib.Lines(), lib.MIME(nil), lib.Printf(), lib.Regexp(nil), lib.Send(nil), lib.Stream(),
		lib.Strings(), lib.Time(), lib.Try(), xml,
	)...)
	if err != nil {
		t.Fatalf("cel.NewEnv: %v", err)
	}

	var names []string
	for name := range all.Functions() {
		_, ok := base.Functions()[name]
		if !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	known := make(map[string]bool)
	for _, name := range names {
		known[name] = true
		if !impure[name] && !pure[name] {
			t.Errorf("%s is in neither impure nor pure", name)
		}
	}
	for name := range impure {
		if !known[name] {
			t.Errorf("impure has %s, which the mito library does not declare", name)
		}
		if pure[name] {
			t.Errorf("%s is in both impure and pure", name)
		}
	}
	for name := range pure {
		if !known[name] {
			t.Errorf("pure has %s, which the mito library does not declare", name)
		}
	}
}
