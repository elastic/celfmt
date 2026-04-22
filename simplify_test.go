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

	"github.com/elastic/mito/lib"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/decls"
	"github.com/google/cel-go/common/types"
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
		{
			name: "has_ternary_comment_on_access",
			in:   "has(x.f) ?\n\t// keep this\n\tx.f\n:\n\t0",
			want: "has(x.f) ?\n\t// keep this\n\tx.f\n:\n\t0",
			opts: []FormatOption{Pretty()},
		},
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
		cel.OptionalTypes(),
		cel.EnableMacroCallTracking(),
	)
	if err != nil {
		t.Fatalf("cel.NewEnv: %v", err)
	}
	return env
}
