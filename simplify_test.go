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
		// The initial value uses the name that is bound.
		{name: "as_self_reference", in: `x.size().as(x, x + 1)`, want: `x.size() + 1`},
		{name: "as_rebound_unused_outer", in: `x.c.as(c, x.drop("c").as(x, x.map(k, k)))`, want: `x.drop("c").map(k, k)`},
		{name: "as_rebound_used_outer", in: `x.c.as(c, x.drop("c").as(x, c.map(k, k)))`, want: `x.c.map(k, k)`},
		// An inlined .as() in a branch of a conditional in an .as() that stays.
		{name: "as_in_conditional_branch", in: `x.as(r, r.b ? r.c.as(v, {"k": v.d}) : {})`, want: `x.as(r, r.b ? {"k": r.c.d} : {})`},
		{name: "as_in_conditional_branch_filter_map", in: `x.as(r, r.b ? r.i.filter(v, v > 0).map(v, v * 2).as(q, {"k": q}) : r.i)`, want: `x.as(r, r.b ? {"k": r.i.map(v, v > 0, v * 2)} : r.i)`},

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
		{name: "has_ternary_deep", in: `has(x.f.g) ? x.f.g : 0`, want: `x.f.?g.orValue(0)`},
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

// TestSimplifyDeterministic checks that the output does not depend on the
// order in which the macro calls are visited, which is not specified.
func TestSimplifyDeterministic(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{
			// Inlining the first .as() leaves its macro call registered
			// under both the original and the substituted ID, and the
			// formatter must choose between them in a fixed way.
			name: "macro_call_ids",
			in: "x.?cursor.next_link.orValue(\"\").as(next_link,\n" +
				"\t(next_link != \"\") ?\n" +
				"\t\tnext_link\n" +
				"\t:\n" +
				"\t\t\"z\"\n" +
				").as(out,\n" +
				"\ty.as(w,\n" +
				"\t\tw + out\n" +
				"\t)\n" +
				")",
		},
		{
			// Inlining c first would put the outer x into the scope
			// of the inner x.
			name: "rebound_name",
			in:   `x.c.as(c, x.drop("c").as(x, c.map(k, {"m": x.with({"k": k})})))`,
		},
	}

	env := newTestEnv(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := func() string {
				compiled, iss := env.Compile(tt.in)
				if iss != nil {
					t.Fatalf("Compile(%q): %v", tt.in, iss)
				}
				native := compiled.NativeRep()
				src := common.NewTextSource(tt.in)
				Simplify(native, src)
				var buf strings.Builder
				err := Format(&buf, native, src, Pretty())
				if err != nil {
					t.Fatalf("Format() after Simplify(%q): %v", tt.in, err)
				}
				return buf.String()
			}

			// The unordered iteration differed about one time in eight.
			want := format()
			for i := 0; i < 500; i++ {
				got := format()
				if got != want {
					t.Fatalf("Simplify(%q) is not deterministic:\nfirst: %q\nrun %d: %q", tt.in, want, i+1, got)
				}
			}
		})
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
