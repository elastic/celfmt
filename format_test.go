// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package celfmt

import (
	"strings"
	"testing"

	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/parser"

	"google.golang.org/protobuf/proto"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name               string
		in                 string
		out                interface{}
		requiresMacroCalls bool
		unparserOptions    []FormatOption
	}{
		{name: "call_add", in: `a + b - c`},
		{name: "call_and", in: `a && b && c && d && e`},
		{name: "call_and_or", in: `a || b && (c || d) && e`},
		{name: "call_cond", in: `a ? b : c`},
		{name: "call_cond_nested_inner", in: `a ? (c ? d : e) : b`},
		{name: "call_cond_nested_outer", in: `a ? b : c ? d : e`, out: `a ? b : (c ? d : e)`},
		{name: "call_index", in: `a[1]["b"]`},
		{name: "call_index_eq", in: `x["a"].single_int32 == 23`},
		{name: "call_mul", in: `a * (b / c) % 0`},
		{name: "call_mul_add", in: `a + b * c`},
		{name: "call_mul_add_nested", in: `(a + b) * c / (d - e)`},
		{name: "call_mul_nested", in: `a * b / c % 0`},
		{name: "call_not", in: `!true`},
		{name: "call_neg", in: `-num`},
		{name: "call_or", in: `a || b || c || d || e`},
		{name: "call_neg_mult", in: `-(1 * 2)`},
		{name: "call_neg_add", in: `-(1 + 2)`},
		{name: "call_operator_precedence", in: `1 - (2 == -1)`},
		{name: "calc_distr_paren", in: `(1 + 2) * 3`},
		{name: "calc_distr_noparen", in: `1 + 2 * 3`},
		{name: "cond_tern_simple", in: `(x > 5) ? (x - 5) : 0`},
		{name: "cond_tern_neg_expr", in: `-((x > 5) ? (x - 5) : 0)`},
		{name: "cond_tern_neg_term", in: `-x ? (x - 5) : 0`},
		{name: "func_global", in: `size(a ? (b ? c : d) : e)`},
		{name: "func_member", in: `a.hello("world")`},
		{name: "func_no_arg", in: `zero()`},
		{name: "func_one_arg", in: `one("a")`},
		{name: "func_two_args", in: `and(d, 32u)`},
		{name: "func_var_args", in: `max(a, b, 100)`},
		{name: "func_neq", in: `x != "a"`},
		{name: "func_in", in: `a in b`},
		{name: "list_empty", in: `[]`},
		{name: "list_one", in: `[1]`},
		{name: "list_ints", in: `[1, 2, 3]`},
		{name: "list_doubles", in: `[1.0, 2.0, 3.0]`},
		{name: "list_doubles", in: `[1.1, 2.1, 3.1]`},
		{name: "list_uints", in: `[1u, 2u, 3u]`},
		{name: "list_numeric", in: `[1, 2.0, 3u]`},
		{name: "list_many", in: `["hello, world", "goodbye, world", "sure, why not?"]`},
		{name: "lit_bytes", in: `b"\303\203\302\277"`},
		{name: "lit_double", in: `-42.101`},
		{name: "lit_false", in: `false`},
		{name: "lit_int", in: `-405069`},
		{name: "lit_null", in: `null`},
		{name: "lit_string", in: `"hello:\t'world'"`},
		{name: "lit_string_quote", in: `"hello:\"world\""`},
		{name: "lit_true", in: `true`},
		{name: "lit_uint", in: `42u`},
		{name: "ident", in: `my_ident`},
		{name: "macro_has", in: `has(hello.world)`},
		{name: "map_empty", in: `{}`},
		{name: "map_lit_key", in: `{"a": a.b.c, b"\142": bytes(a.b.c)}`},
		{name: "map_expr_key", in: `{a: a, b: a.b, c: a.b.c, a ? b : c: false, a || b: true}`},
		{name: "msg_empty", in: `v1alpha1.Expr{}`},
		{name: "msg_fields", in: `v1alpha1.Expr{id: 1, call_expr: v1alpha1.Call_Expr{function: "name"}}`},
		{name: "select", in: `a.b.c`},
		{name: "idx_idx_sel", in: `a[b][c].name`},
		{name: "sel_expr_target", in: `(a + b).name`},
		{name: "sel_cond_target", in: `(a ? b : c).name`},
		{name: "idx_cond_target", in: `(a ? b : c)[0]`},
		{name: "cond_conj", in: `(a1 && a2) ? b : c`},
		{name: "cond_disj_conj", in: `a ? (b1 || b2) : (c1 && c2)`},
		{name: "call_cond_target", in: `(a ? b : c).method(d)`},
		{name: "cond_flat", in: `false && !true || false`},
		{name: "cond_paren", in: `false && (!true || false)`},
		{name: "cond_cond", in: `(false && !true || false) ? 2 : 3`},
		{name: "cond_binop", in: `(x < 5) ? x : 5`},
		{name: "cond_binop_binop", in: `(x > 5) ? (x - 5) : 0`},
		{name: "cond_cond_binop", in: `(x > 5) ? ((x > 10) ? (x - 10) : 5) : 0`},
		{name: "select_opt", in: `a.?b`},
		{name: "index_opt", in: `a[?b]`},
		{name: "list_lit_opt", in: `[?a, ?b, c]`},
		{name: "map_lit_opt", in: `{?a: b, c: d}`},
		{name: "msg_fields_opt", in: `v1alpha1.Expr{?id: id, call_expr: v1alpha1.Call_Expr{function: "name"}}`},

		// Equivalent expressions form unparse which do not match the originals.
		{name: "call_add_equiv", in: `a+b-c`, out: `a + b - c`},
		{name: "call_cond_equiv", in: `a ? b          : c`, out: `a ? b : c`},
		{name: "call_index_equiv", in: `a[  1  ]["b"]`, out: `a[1]["b"]`},
		{name: "call_or_and_equiv", in: `(false && !true) || false`, out: `false && !true || false`},
		{name: "call_not_not_equiv", in: `!!true`, out: `true`},
		{name: "call_cond_equiv", in: `(a || b ? c : d).e`, out: `((a || b) ? c : d).e`},
		{name: "lit_quote_bytes_equiv", in: `b'aaa"bbb'`, out: `b"\141\141\141\042\142\142\142"`},
		{name: "select_equiv", in: `a . b . c`, out: `a.b.c`},

		// These expressions require macro call tracking to be enabled.
		{
			name:               "comp_all",
			in:                 `[1, 2, 3].all(x, x > 0)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_exists",
			in:                 `[1, 2, 3].exists(x, x > 0)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_map",
			in:                 `[1, 2, 3].map(x, x >= 2, x * 4)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_exists_one",
			in:                 `[1, 2, 3].exists_one(x, x >= 2)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_nested",
			in:                 `[[1], [2], [3]].map(x, x.filter(y, y > 1))`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_chained",
			in:                 `[1, 2, 3].map(x, x >= 2, x * 4).filter(x, x <= 10)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_chained_opt",
			in:                 `[?a, b[?0], c].map(x, x >= 2, x * 4).filter(x, x <= 10)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_map_opt",
			in:                 `{?a: b[?0]}.map(k, x >= 2, x * 4)`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_map_opt",
			in:                 `{a: has(b.c)}.exists(k, k != "")`,
			requiresMacroCalls: true,
		},
		{
			name:               "comp_nested",
			in:                 `{a: [1, 2].all(i > 0)}.exists(k, k != "")`,
			requiresMacroCalls: true,
		},

		// These expressions will not be wrapped because they haven't met the
		// conditions required by the provided unparser options
		{
			name: "call_no_wrap_no_operators",
			in:   "a + b + c + d",
			out:  "a + b + c + d",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
			},
		},
		{
			name: "call_no_wrap_column_limit_large_val",
			in:   "a + b + c + d",
			out:  "a + b + c + d",
			unparserOptions: []FormatOption{
				WrapOnColumn(1000),
				WrapOnOperators(operators.Add),
			},
		},
		{
			name: "call_no_wrap_column_limit_equal_length_to_input",
			in:   "a + b + c + d",
			out:  "a + b + c + d",
			unparserOptions: []FormatOption{
				WrapOnColumn(13),
				WrapOnOperators(operators.Add),
			},
		},

		// These expressions will be formatted based on the unparser options provided
		{
			name: "call_wrap_add",
			in:   "a + b - d * e",
			out:  "a +\nb - d * e",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Add),
			},
		},
		{
			name: "call_wrap_add_subtract",
			in:   "a * b + c - d * e",
			out:  "a * b +\nc -\nd * e",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Add, operators.Subtract),
			},
		},
		{
			name: "call_wrap_add_subtract",
			in:   "a * b + c - d * e",
			out:  "a * b +\nc -\nd * e",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Add, operators.Subtract),
			},
		},
		{
			name: "call_wrap_logical_and",
			in:   "a && b && c && d && e",
			out:  "a &&\nb &&\nc &&\nd &&\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.LogicalAnd),
			},
		},
		{
			name: "call_wrap_logical_and_2",
			in:   "a && b",
			out:  "a &&\nb",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.LogicalAnd),
			},
		},
		{
			name: "call_wrap_conditional",
			in:   "a ? b : c ? d : e",
			out:  "a ?\nb : (c ?\nd : e)",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Conditional),
			},
		},
		{
			name: "call_wrap_or",
			in:   "a || b || c || d || e",
			out:  "a ||\nb ||\nc ||\nd ||\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.LogicalOr),
			},
		},
		{
			name: "call_wrap_equals",
			in:   "a == b == c == d == e",
			out:  "a ==\nb ==\nc ==\nd ==\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Equals),
			},
		},
		{
			name: "call_wrap_greater",
			in:   "a > b > c > d > e",
			out:  "a >\nb >\nc >\nd >\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Greater),
			},
		},
		{
			name: "call_wrap_greater_equals",
			in:   "a >= b >= c >= d >= e",
			out:  "a >=\nb >=\nc >=\nd >=\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.GreaterEquals),
			},
		},
		{
			name: "call_wrap_in",
			in:   "a in b in c in d in e",
			out:  "a in\nb in\nc in\nd in\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.In),
			},
		},
		{
			name: "call_wrap_less",
			in:   "a < b < c < d < e",
			out:  "a <\nb <\nc <\nd <\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Less),
			},
		},
		{
			name: "call_wrap_less_equals",
			in:   "a <= b <= c <= d <= e",
			out:  "a <=\nb <=\nc <=\nd <=\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.LessEquals),
			},
		},
		{
			name: "call_wrap_not_equals",
			in:   "a != b != c != d != e",
			out:  "a !=\nb !=\nc !=\nd !=\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.NotEquals),
			},
		},
		{
			name: "call_wrap_divide",
			in:   "a / b / c / d / e",
			out:  "a /\nb /\nc /\nd /\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Divide),
			},
		},
		{
			name: "call_wrap_modulo",
			in:   "a % b % c % d % e",
			out:  "a %\nb %\nc %\nd %\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Modulo),
			},
		},
		{
			name: "call_wrap_multiply",
			in:   "a * b * c * d * e",
			out:  "a *\nb *\nc *\nd *\ne",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Multiply),
			},
		},
		{
			name: "call_wrap_logical_and_long_variables",
			in:   "longVariableA && longVariableB && longVariableC",
			out:  "longVariableA &&\nlongVariableB &&\nlongVariableC",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.LogicalAnd),
			},
		},
		{
			name:               "comp_chained_wrap_comparisons",
			in:                 "[1, 2, 3].map(x, x >= 2, x * 4).filter(x, x <= 10)",
			out:                "[1, 2, 3].map(x, x >=\n2, x * 4).filter(x, x <=\n10)",
			requiresMacroCalls: true,
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.GreaterEquals, operators.LessEquals),
			},
		},
		{
			name: "call_wrap_before_add",
			in:   "a + b - d * e",
			out:  "a\n+ b - d * e",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Add),
				WrapAfterColumnLimit(false),
			},
		},
		{
			name: "call_wrap_before_add_subtract",
			in:   "a * b + c - d * e",
			out:  "a * b\n+ c\n- d * e",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.Add, operators.Subtract),
				WrapAfterColumnLimit(false),
			},
		},
		{
			name: "call_wrap_logical_and_long_variables",
			in:   "longVariableA && longVariableB && longVariableC",
			out:  "longVariableA\n&& longVariableB\n&& longVariableC",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
				WrapOnOperators(operators.LogicalAnd),
				WrapAfterColumnLimit(false),
			},
		},
		{
			name: "call_wrap_logical_and_long_input",
			in:   `"my-principal-group" in request.auth.claims && request.auth.claims.iat > now - duration("5m")`,
			out:  `"my-principal-group" in request.auth.claims &&` + "\n" + `request.auth.claims.iat > now - duration("5m")`,
			unparserOptions: []FormatOption{
				WrapOnColumn(40),
				WrapOnOperators(operators.LogicalAnd),
			},
		},
		{
			name: "call_wrap_before_logical_and_long_input",
			in:   `"my-principal-group" in request.auth.claims && request.auth.claims.iat > now - duration("5m")`,
			out:  `"my-principal-group" in request.auth.claims` + "\n" + `&& request.auth.claims.iat > now - duration("5m")`,
			unparserOptions: []FormatOption{
				WrapOnColumn(40),
				WrapOnOperators(operators.LogicalAnd),
				WrapAfterColumnLimit(false),
			},
		},
		{
			// By default:
			// - Column limit is at 80
			// - && and || are wrapped
			// - Wrapping occurs after the symbol
			name: "call_wrap_default",
			in:   `jwt.extra_claims.filter(c, c.startsWith("group")).all(c, jwt.extra_claims[c].all(g, g.endsWith("@acme.co"))) && jwt.extra_claims.exists(c, c.startsWith("group")) || request.auth.claims.group == "admin" || request.auth.principal == "user:me@acme.co"`,
			out: `jwt.extra_claims.filter(c, c.startsWith("group")).all(c, jwt.extra_claims[c].all(g, g.endsWith("@acme.co"))) &&` +
				"\n" +
				`jwt.extra_claims.exists(c, c.startsWith("group")) || request.auth.claims.group == "admin" ||` +
				"\n" +
				`request.auth.principal == "user:me@acme.co"`,
			requiresMacroCalls: true,
		},
		{
			// && and || are wrapped by default if only the column limit is specified
			name: "call_wrap_default_operators",
			in:   "longVariableA && longVariableB || longVariableC + longVariableD - longVariableE",
			out:  "longVariableA &&\nlongVariableB ||\nlongVariableC + longVariableD - longVariableE",
			unparserOptions: []FormatOption{
				WrapOnColumn(3),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prsr, err := parser.NewParser(
				parser.Macros(parser.AllMacros...),
				parser.PopulateMacroCalls(test.requiresMacroCalls),
				parser.EnableOptionalSyntax(true),
			)
			if err != nil {
				t.Fatalf("NewParser() failed: %v", err)
			}
			src := common.NewTextSource(test.in)
			p, iss := prsr.Parse(src)
			if len(iss.GetErrors()) > 0 {
				t.Fatalf("parser.Parse(%s) failed: %v", test.in, iss.ToDisplayString())
			}
			var buf strings.Builder
			err = Format(&buf, p, src, test.unparserOptions...)
			if err != nil {
				t.Fatalf("Format(%s) failed: %v", test.in, err)
			}
			var want interface{} = test.in
			if test.out != nil {
				want = test.out
			}
			out := buf.String()
			if out != want {
				t.Errorf("Format() got '%s', wanted '%s'", out, want)
			}
			src = common.NewTextSource(out)
			p2, iss := prsr.Parse(src)
			if len(iss.GetErrors()) > 0 {
				t.Fatalf("parser.Parse(%s) roundtrip failed: %v", test.in, iss.ToDisplayString())
			}
			before, err := ast.ExprToProto(p.Expr())
			if err != nil {
				t.Fatalf("ast.ExprToProto() failed: %v", err)
			}
			after, err := ast.ExprToProto(p2.Expr())
			if err != nil {
				t.Fatalf("ast.ExprToProto() failed: %v", err)
			}
			if !proto.Equal(before, after) {
				t.Errorf("Roundtrip Parse() differs from original. Got '%v', wanted '%v'", before, after)
			}
		})
	}
}
