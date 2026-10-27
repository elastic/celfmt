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
		{name: "as_inner_inlined_outer_multi_use", in: `a.as(v, (v + v).as(w, w + 1))`, want: `a.as(v, v + v + 1)`},
		{name: "as_init_filter_map", in: `x.filter(v, v > 0).map(v, v * 2).as(w, w)`, want: `x.map(v, v > 0, v * 2)`},
		// The initial value uses the name that is bound.
		{name: "as_self_reference", in: `x.size().as(x, x + 1)`, want: `x.size() + 1`},
		{name: "as_rebound_unused_outer", in: `x.c.as(c, x.drop("c").as(x, x.map(k, k)))`, want: `x.drop("c").map(k, k)`},
		{name: "as_rebound_used_outer", in: `x.c.as(c, x.drop("c").as(x, c.map(k, k)))`, want: `x.c.map(k, k)`},

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
		{name: "has_ternary_in_as_init", in: `(has(x.f) ? x.f : 0).as(v, v + v)`, want: `(has(x.f) ? x.f : 0).as(v, v + v)`},
		{name: "has_ternary_in_map_body", in: `x.map(v, has(v.f) ? v.f : 0)`, want: `x.map(v, has(v.f) ? v.f : 0)`},
		{name: "has_ternary_deep", in: `has(x.f.g) ? x.f.g : 0`, want: `x.f.?g.orValue(0)`},
		{name: "has_ternary_negated", in: `!has(x.f) ? "default" : x.f`, want: `x.?f.orValue("default")`},
		{name: "has_ternary_no_match_op", in: `has(x.f) ? x.f + 1 : 0`, want: `has(x.f) ? (x.f + 1) : 0`},
		{name: "has_ternary_no_match_field", in: `has(x.f) ? x.g : 0`, want: `has(x.f) ? x.g : 0`},
		{name: "has_ternary_no_match_operand", in: `has(x.f) ? y.f : 0`, want: `has(x.f) ? y.f : 0`},
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
		cel.OptionalTypes(cel.OptionalTypesVersion(lib.OptionalTypesVersion)),
		ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(lib.OptionalTypesVersion)),
		cel.EnableMacroCallTracking(),
	)
	if err != nil {
		t.Fatalf("cel.NewEnv: %v", err)
	}
	return env
}
