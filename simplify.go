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

	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

// Simplify applies semantics-preserving simplifications to the AST:
//   - inline single-use .as() bindings
//   - eliminate boolean comparisons (x == true → x, x == false → !x)
//   - rewrite has(x.f) ? x.f : d and !has(x.f) ? d : x.f → x.?f.orValue(d)
//   - fold filter into comprehension: x.filter(v,c).map(v,t) → x.map(v,c,t)
func Simplify(a *ast.AST, src common.Source) {
	inlineAs(a)
	elimBoolCmp(a)
	elimHasTernary(a, src)
	foldFilter(a)
}

// inlineAs finds .as() macro calls where the bound variable is used at most
// once in the result expression, and replaces the comprehension with its
// result (substituting the init expression for the single use). Zero-use
// bindings are replaced with the bare result.
func inlineAs(a *ast.AST) {
	info := a.SourceInfo()
	// Each inlining mutates the macro call map (clear, set, clear), so
	// restart iteration after every change to avoid depending on Go's
	// unspecified map-iteration-after-mutation behaviour.
	for {
		inlined := false
		for id, call := range info.MacroCalls() {
			if call.Kind() != ast.CallKind {
				continue
			}
			if call.AsCall().FunctionName() != "as" {
				continue
			}
			var comp ast.Expr
			ast.PreOrderVisit(a.Expr(), ast.NewExprVisitor(func(e ast.Expr) {
				if e.ID() == id {
					comp = e
				}
			}))
			if comp == nil || comp.Kind() != ast.ComprehensionKind {
				continue
			}
			c := comp.AsComprehension()
			name := c.AccuVar()
			init := c.AccuInit()
			result := c.Result()

			n := countIdent(result, name)
			if n > 1 {
				continue
			}

			if n == 1 {
				substituteIdent(result, name, init, info)
			}

			// Clear the outer .as() macro entry before any remap so we
			// don't clobber a remapped inner entry that lands on the
			// same ID.
			info.ClearMacroCall(id)

			// If the result is a comprehension with its own macro call,
			// the formatter will look it up by ID. After SetKindCase the
			// content moves to comp's ID, so remap the entry (and
			// substitute in the macro call expression, which is a
			// separate tree).
			if mcall, ok := info.GetMacroCall(result.ID()); ok {
				if n == 1 {
					substituteIdent(mcall, name, init, info)
				}
				info.SetMacroCall(comp.ID(), mcall)
				info.ClearMacroCall(result.ID())
			}

			comp.SetKindCase(result)
			inlined = true
			break
		}
		if !inlined {
			return
		}
	}
}

// countIdent counts IdentKind nodes in expr whose name matches ident.
func countIdent(expr ast.Expr, ident string) int {
	var n int
	ast.PreOrderVisit(expr, ast.NewExprVisitor(func(e ast.Expr) {
		if e.Kind() == ast.IdentKind && e.AsIdent() == ident {
			n++
		}
	}))
	return n
}

// countIdentInMacroTree counts IdentKind nodes named ident, recurring into
// nested macro call expressions reachable via the SourceInfo macro call map.
func countIdentInMacroTree(info *ast.SourceInfo, expr ast.Expr, ident string) int {
	var n int
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		ast.PreOrderVisit(e, ast.NewExprVisitor(func(node ast.Expr) {
			if node.Kind() == ast.IdentKind && node.AsIdent() == ident {
				n++
			}
			if mcall, ok := info.GetMacroCall(node.ID()); ok {
				walk(mcall)
			}
		}))
	}
	walk(expr)
	return n
}

// substituteIdent replaces all IdentKind nodes named ident with replacement.
// If info is non-nil and replacement has a macro call, the macro call is
// copied to each substitution site so the formatter can find it by ID.
func substituteIdent(expr ast.Expr, ident string, replacement ast.Expr, info *ast.SourceInfo) {
	ast.PreOrderVisit(expr, ast.NewExprVisitor(func(e ast.Expr) {
		if e.Kind() == ast.IdentKind && e.AsIdent() == ident {
			e.SetKindCase(replacement)
			if info != nil {
				if mcall, ok := info.GetMacroCall(replacement.ID()); ok {
					info.SetMacroCall(e.ID(), mcall)
				}
			}
		}
	}))
}

// substituteIdentInMacroTree replaces IdentKind nodes named ident with
// replacement, recurring into nested macro call expressions.
func substituteIdentInMacroTree(info *ast.SourceInfo, expr ast.Expr, ident string, replacement ast.Expr) {
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		ast.PreOrderVisit(e, ast.NewExprVisitor(func(node ast.Expr) {
			if node.Kind() == ast.IdentKind && node.AsIdent() == ident {
				node.SetKindCase(replacement)
			}
			if mcall, ok := info.GetMacroCall(node.ID()); ok {
				walk(mcall)
			}
		}))
	}
	walk(expr)
}

// elimBoolCmp rewrites x == true → x and x == false → !x.
func elimBoolCmp(a *ast.AST) {
	fac := ast.NewExprFactory()
	ast.PreOrderVisit(a.Expr(), ast.NewExprVisitor(func(e ast.Expr) {
		if e.Kind() != ast.CallKind {
			return
		}
		c := e.AsCall()
		if c.FunctionName() != operators.Equals {
			return
		}
		args := c.Args()
		if len(args) != 2 {
			return
		}
		lhs, rhs := args[0], args[1]
		val, other, ok := boolLiteralOperand(lhs, rhs)
		if !ok {
			return
		}
		if val {
			e.SetKindCase(other)
		} else {
			neg := fac.NewCall(e.ID(), operators.LogicalNot, other)
			e.SetKindCase(neg)
		}
	}))
}

// boolLiteralOperand checks whether exactly one of lhs/rhs is a bool literal
// and returns (literal value, other operand, true). Returns (false, nil, false)
// when neither operand is a bool literal.
func boolLiteralOperand(lhs, rhs ast.Expr) (val bool, other ast.Expr, ok bool) {
	if v, is := asBool(rhs); is {
		return v, lhs, true
	}
	if v, is := asBool(lhs); is {
		return v, rhs, true
	}
	return false, nil, false
}

func asBool(e ast.Expr) (bool, bool) {
	if e.Kind() != ast.LiteralKind {
		return false, false
	}
	switch e.AsLiteral() {
	case types.True:
		return true, true
	case types.False:
		return false, true
	}
	return false, false
}

// elimHasTernary rewrites has(x.f) ? x.f : d → x.?f.orValue(d)
// and !has(x.f) ? d : x.f → x.?f.orValue(d). The rewrite is skipped
// when the field-access branch has preceding comments, since the
// simplified form has no position to attach them.
func elimHasTernary(a *ast.AST, src common.Source) {
	info := a.SourceInfo()
	fac := ast.NewExprFactory()
	ast.PreOrderVisit(a.Expr(), ast.NewExprVisitor(func(e ast.Expr) {
		if e.Kind() != ast.CallKind {
			return
		}
		c := e.AsCall()
		if c.FunctionName() != operators.Conditional {
			return
		}
		args := c.Args()
		if len(args) != 3 {
			return
		}

		hasSel, access, dflt := matchHasTernary(args[0], args[1], args[2])
		if hasSel == nil {
			return
		}
		if hasComment(src, info, access.ID()) {
			return
		}

		sel := hasSel.AsSelect()
		optSel := fac.NewCall(hasSel.ID(), operators.OptSelect,
			sel.Operand(), fac.NewLiteral(access.ID(), types.String(sel.FieldName())))
		orVal := fac.NewMemberCall(e.ID(), "orValue", optSel, dflt)

		e.SetKindCase(orVal)
		info.ClearMacroCall(hasSel.ID())
	}))
}

// hasComment reports whether there are comment lines immediately preceding
// the expression with the given ID in the source text.
func hasComment(src common.Source, info *ast.SourceInfo, id int64) bool {
	start := info.GetStartLocation(id)
	for line := start.Line() - 1; line > 0; line-- {
		text, ok := src.Snippet(line)
		if !ok {
			return false
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "//")
	}
	return false
}

// matchHasTernary checks whether a ternary's condition + branches form a
// has(x.f) ? x.f : d pattern (or its negated form !has(x.f) ? d : x.f).
// Returns (has-test select, field-access select, default) on match, or
// (nil, nil, nil) if the pattern doesn't apply.
func matchHasTernary(cond, trueBranch, falseBranch ast.Expr) (hasSel, access, dflt ast.Expr) {
	switch {
	case isHasTest(cond):
		// has(x.f) ? x.f : d
		hasSel = cond
		access = trueBranch
		dflt = falseBranch
	case isNegatedHasTest(cond):
		// !has(x.f) ? d : x.f
		hasSel = cond.AsCall().Args()[0]
		access = falseBranch
		dflt = trueBranch
	default:
		return nil, nil, nil
	}

	if access.Kind() != ast.SelectKind || access.AsSelect().IsTestOnly() {
		return nil, nil, nil
	}
	h := hasSel.AsSelect()
	a := access.AsSelect()
	if h.FieldName() != a.FieldName() {
		return nil, nil, nil
	}
	if !exprEqual(h.Operand(), a.Operand()) {
		return nil, nil, nil
	}
	return hasSel, access, dflt
}

func isHasTest(e ast.Expr) bool {
	return e.Kind() == ast.SelectKind && e.AsSelect().IsTestOnly()
}

func isNegatedHasTest(e ast.Expr) bool {
	if e.Kind() != ast.CallKind {
		return false
	}
	c := e.AsCall()
	if c.FunctionName() != operators.LogicalNot {
		return false
	}
	args := c.Args()
	return len(args) == 1 && isHasTest(args[0])
}

// foldFilter rewrites filter-then-comprehension chains into a single
// comprehension with a filter argument:
//
//	x.filter(v, cond).map(v, t) → x.map(v, cond, t)
//	x.filter(v, cond).transformList(_, v, t) → x.transformList(_, v, cond, t)
//	x.filter(v, cond).transformMap(_, v, t) → x.transformMap(_, v, cond, t)
//	x.filter(v, cond).transformMapEntry(_, v, t) → x.transformMapEntry(_, v, cond, t)
//
// For two-variable comprehensions the merge is only applied when the first
// iteration variable (index/key) is "_", since the filter changes element
// indices and merging would alter semantics if the index is used.
func foldFilter(a *ast.AST) {
	info := a.SourceInfo()
	fac := ast.NewExprFactory()
	for {
		folded := false
		for id, call := range info.MacroCalls() {
			if call.Kind() != ast.CallKind {
				continue
			}
			c := call.AsCall()
			if !c.IsMemberFunction() {
				continue
			}
			fn := c.FunctionName()
			args := c.Args()

			// Identify comprehensions that accept an optional filter arg
			// and currently lack one (i.e. they have the minimum arg count).
			var valIdx int // index of the value iteration variable in args
			switch fn {
			case "map":
				if len(args) != 2 {
					continue
				}
				valIdx = 0
			case "transformList", "transformMap", "transformMapEntry":
				if len(args) != 3 {
					continue
				}
				valIdx = 1
			default:
				continue
			}

			// The target of the macro call should reference the filter
			// comprehension by ID.
			target := c.Target()
			filterCall, ok := info.GetMacroCall(target.ID())
			if !ok || filterCall.Kind() != ast.CallKind {
				continue
			}
			fc := filterCall.AsCall()
			if fc.FunctionName() != "filter" || !fc.IsMemberFunction() {
				continue
			}
			filterArgs := fc.Args()
			if len(filterArgs) != 2 {
				continue
			}
			filterVar := filterArgs[0]
			filterCond := filterArgs[1]

			// The filter's iteration variable must match the consuming
			// comprehension's value variable, or be safely renameable.
			iterVar := args[valIdx]
			if filterVar.Kind() != ast.IdentKind || iterVar.Kind() != ast.IdentKind {
				continue
			}
			needsRename := filterVar.AsIdent() != iterVar.AsIdent()
			if needsRename && countIdentInMacroTree(info, filterCond, iterVar.AsIdent()) > 0 {
				continue // target name already in condition — rename would capture
			}

			// For two-variable comprehensions the first variable (key/index)
			// must be unused; otherwise the merge changes semantics because
			// the filter alters element indices.
			if valIdx == 1 {
				first := args[0]
				if first.Kind() != ast.IdentKind || first.AsIdent() != "_" {
					continue
				}
			}

			// Build the merged macro call: target.fn(args..., cond, transform).
			filterTarget := fc.Target()
			if needsRename {
				substituteIdentInMacroTree(info, filterCond, filterVar.AsIdent(), fac.NewIdent(0, iterVar.AsIdent()))
			}
			var newArgs []ast.Expr
			switch fn {
			case "map":
				newArgs = []ast.Expr{iterVar, filterCond, args[1]}
			default:
				newArgs = []ast.Expr{args[0], iterVar, filterCond, args[2]}
			}
			newMCall := fac.NewMemberCall(0, fn, filterTarget, newArgs...)
			info.SetMacroCall(id, newMCall)
			info.ClearMacroCall(target.ID())

			// Update the AST: skip the filter comprehension so the
			// consuming comprehension iterates directly over the
			// filter's source.
			ast.PreOrderVisit(a.Expr(), ast.NewExprVisitor(func(e ast.Expr) {
				if e.ID() != id || e.Kind() != ast.ComprehensionKind {
					return
				}
				iterRange := e.AsComprehension().IterRange()
				if iterRange.Kind() != ast.ComprehensionKind {
					return
				}
				filterSource := iterRange.AsComprehension().IterRange()
				if mcall, ok := info.GetMacroCall(filterSource.ID()); ok {
					info.SetMacroCall(iterRange.ID(), mcall)
					info.ClearMacroCall(filterSource.ID())
				}
				iterRange.SetKindCase(filterSource)
			}))

			folded = true
			break
		}
		if !folded {
			return
		}
	}
}

// exprEqual reports whether two expression trees are structurally identical,
// ignoring node IDs. Returns false for any kind it cannot compare.
func exprEqual(a, b ast.Expr) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case ast.IdentKind:
		return a.AsIdent() == b.AsIdent()
	case ast.SelectKind:
		sa, sb := a.AsSelect(), b.AsSelect()
		return sa.FieldName() == sb.FieldName() &&
			sa.IsTestOnly() == sb.IsTestOnly() &&
			exprEqual(sa.Operand(), sb.Operand())
	case ast.LiteralKind:
		return a.AsLiteral() == b.AsLiteral()
	case ast.CallKind:
		ca, cb := a.AsCall(), b.AsCall()
		if ca.FunctionName() != cb.FunctionName() {
			return false
		}
		if ca.IsMemberFunction() != cb.IsMemberFunction() {
			return false
		}
		if ca.IsMemberFunction() && !exprEqual(ca.Target(), cb.Target()) {
			return false
		}
		aa, ab := ca.Args(), cb.Args()
		if len(aa) != len(ab) {
			return false
		}
		for i := range aa {
			if !exprEqual(aa[i], ab[i]) {
				return false
			}
		}
		return true
	case ast.ListKind:
		la, lb := a.AsList(), b.AsList()
		ea, eb := la.Elements(), lb.Elements()
		if len(ea) != len(eb) {
			return false
		}
		for i := range ea {
			if !exprEqual(ea[i], eb[i]) {
				return false
			}
		}
		return true
	case ast.MapKind:
		ma, mb := a.AsMap(), b.AsMap()
		ea, eb := ma.Entries(), mb.Entries()
		if len(ea) != len(eb) {
			return false
		}
		for i := range ea {
			ka, kb := ea[i].AsMapEntry(), eb[i].AsMapEntry()
			if !exprEqual(ka.Key(), kb.Key()) || !exprEqual(ka.Value(), kb.Value()) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
