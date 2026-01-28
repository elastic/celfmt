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

// Package celfmt provides formatting for CEL programs.
package celfmt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

// Format takes an input expression and source information and generates a human-readable expression,
// writing it to dst.
//
// Note, formatting an AST will often generate the same expression as was originally parsed, but some
// formatting may be lost in translation, notably:
//
// - All quoted literals are doubled quoted unless triple quoted string literal syntax is used.
// - Byte literals are represented as octal escapes (same as Google SQL) unless using triple quotes.
// - Floating point values are converted to the small number of digits needed to represent the value.
// - Spacing around punctuation marks may be lost.
// - Parentheses will only be applied when they affect operator precedence.
//
// This function optionally takes in one or more UnparserOption to alter the formatting behavior, such as
// performing word wrapping on expressions.
func Format(dst io.Writer, ast *ast.AST, src common.Source, opts ...FormatOption) error {
	unparserOpts := &unparserOption{
		wrapOnColumn:         defaultWrapOnColumn,
		wrapAfterColumnLimit: defaultWrapAfterColumnLimit,
		operatorsToWrapOn:    defaultOperatorsToWrapOn,
		indent:               defaultIndentString,
	}

	var err error
	for _, opt := range opts {
		unparserOpts, err = opt(unparserOpts)
		if err != nil {
			return err
		}
	}
	expr := ast.Expr()
	un := &formatter{
		dst:      lenWriter{w: dst, indent: unparserOpts.indent},
		src:      src,
		info:     ast.SourceInfo(),
		options:  unparserOpts,
		comments: make(map[location]int64),
	}
	err = un.visit(expr, false)
	if err != nil {
		return err
	}
	return nil
}

// formatter visits an expression to reconstruct a human-readable string from an AST.
type formatter struct {
	dst              lenWriter
	src              common.Source
	info             *ast.SourceInfo
	options          *unparserOption
	lastWrappedIndex int

	indent int

	comments map[location]int64

	err error
}

type lenWriter struct {
	w   io.Writer
	len int

	prefix bytes.Buffer
	indent string
}

func (w *lenWriter) WriteString(s string) (int, error) {
	var b []byte
	if strings.ContainsFunc(s, func(r rune) bool {
		return !unicode.IsSpace(r)
	}) {
		b = w.prefix.Bytes()
	} else if w.prefix.Len() != 0 {
		b = []byte{'\n'}
	}
	w.prefix.Reset()
	_, err := w.w.Write(b)
	if err != nil {
		return 0, err
	}
	n, err := io.WriteString(w.w, s)
	w.len += n
	return n, err
}

func (w *lenWriter) WriteNewLine(indent int) (int, error) {
	w.prefix.Reset()
	n, err := w.prefix.WriteString("\n" + strings.Repeat(w.indent, indent))
	w.len += n
	return n, err
}

func (w *lenWriter) Len() int { return w.len }

type location struct {
	line, col int
}

func (un *formatter) WriteString(s string) (int, error) {
	if un.err != nil {
		return 0, un.err
	}
	var n int
	n, un.err = un.dst.WriteString(s)
	return n, un.err
}

func (un *formatter) WriteNewLine() (int, error) {
	if un.err != nil {
		return 0, un.err
	}
	if !un.options.pretty {
		return 0, nil
	}
	un.lastWrappedIndex = un.dst.Len()
	var n int
	n, un.err = un.dst.WriteNewLine(un.indent)
	return n, un.err
}

func (un *formatter) CommentBlock(id int64) []string {
	if !un.options.pretty {
		return nil
	}
	var comments []string
	start := un.info.GetStartLocation(id)
	first := true // ¯\_(ツ)_/¯ The AST's position information is weaker than is ideal.
	wasBlank := false
	for line := start.Line(); line > 0; line-- {
		text, ok := un.src.Snippet(line)
		comment := strings.TrimSpace(text)
		if !ok || (comment != "" && !strings.HasPrefix(comment, "//")) {
			if first {
				first = false
				continue
			}
			break
		}
		first = false
		loc := location{line, strings.Index(text, comment)}
		if cid, ok := un.comments[loc]; ok {
			if cid != id {
				return nil
			}
			break
		}
		un.comments[loc] = id
		if comment != "" {
			// Remove comment prefix and any leading non-tab spaces.
			comment = strings.TrimPrefix(comment, "//")
			idx := strings.IndexFunc(comment, func(r rune) bool {
				return r == '\t' || !unicode.IsSpace(r)
			})
			if idx >= 0 {
				comment = comment[idx:]
			}
			// Remove all trailing white space.
			comment = strings.TrimRight(comment, " \t\n\v\f\r\u0085\u00a0")
			// Format comment with a leading space between text and mark.
			if comment == "" {
				comment = "//"
			} else {
				comment = "// " + comment
			}
			comments = append(comments, comment)
			wasBlank = false
		} else if !wasBlank {
			comments = append(comments, "")
			wasBlank = true
		}
	}
	slices.Reverse(comments)
	return comments
}

func (un *formatter) Comment(id int64) string {
	if !un.options.pretty {
		return ""
	}
	start := un.info.GetStartLocation(id)
	text, ok := un.src.Snippet(start.Line())
	if !ok {
		return ""
	}
	stop := un.info.GetStopLocation(id)
	if stop.Column() <= len(text) {
		text = text[stop.Column():]
	}
	idx := strings.LastIndex(text, "//")
	if idx < 0 {
		return ""
	}
	loc := location{start.Line(), stop.Column() + idx}
	if _, ok := un.comments[loc]; ok {
		return ""
	}
	un.comments[loc] = id
	return " // " + strings.TrimSpace(strings.TrimPrefix(text[idx:], "//"))
}

func (un *formatter) visit(expr ast.Expr, macro bool) error {
	if un.err != nil {
		return un.err
	}
	if expr == nil {
		return errors.New("unsupported expression")
	}

	for _, c := range un.CommentBlock(expr.ID()) {
		un.WriteString(c)
		un.WriteNewLine()
	}

	visited, err := un.visitMaybeMacroCall(expr)
	if visited || err != nil {
		return err
	}
	switch expr.Kind() {
	case ast.CallKind:
		return un.visitCall(expr, macro)
	case ast.LiteralKind:
		return un.visitConst(expr)
	case ast.IdentKind:
		return un.visitIdent(expr)
	case ast.ListKind:
		return un.visitList(expr)
	case ast.MapKind:
		return un.visitStructMap(expr)
	case ast.SelectKind:
		return un.visitSelect(expr)
	case ast.StructKind:
		return un.visitStructMsg(expr)
	default:
		return fmt.Errorf("unsupported expression: %v", expr.Kind())
	}
}

func (un *formatter) visitCall(expr ast.Expr, macro bool) error {
	c := expr.AsCall()
	fun := c.FunctionName()
	switch fun {
	// ternary operator
	case operators.Conditional:
		return un.visitCallConditional(expr)
	// optional select operator
	case operators.OptSelect:
		return un.visitOptSelect(expr)
	// index operator
	case operators.Index:
		return un.visitCallIndex(expr)
	// optional index operator
	case operators.OptIndex:
		return un.visitCallOptIndex(expr)
	// unary operators
	case operators.LogicalNot, operators.Negate:
		return un.visitCallUnary(expr)
	// binary operators
	case operators.Add,
		operators.Divide,
		operators.Equals,
		operators.Greater,
		operators.GreaterEquals,
		operators.In,
		operators.Less,
		operators.LessEquals,
		operators.LogicalAnd,
		operators.LogicalOr,
		operators.Modulo,
		operators.Multiply,
		operators.NotEquals,
		operators.OldIn,
		operators.Subtract:
		return un.visitCallBinary(expr)
	// standard function calls.
	default:
		return un.visitCallFunc(expr, macro)
	}
}

func (un *formatter) visitCallBinary(expr ast.Expr) error {
	c := expr.AsCall()
	fun := c.FunctionName()
	args := c.Args()
	lhs := args[0]
	// add parens if the current operator is lower precedence than the lhs expr operator.
	lhsParen := isComplexOperatorWithRespectTo(fun, lhs)
	rhs := args[1]
	// add parens if the current operator is lower precedence than the rhs expr operator,
	// or the same precedence and the operator is left recursive.
	rhsParen := isComplexOperatorWithRespectTo(fun, rhs)
	if !rhsParen && isLeftRecursive(fun) {
		rhsParen = isSamePrecedence(fun, rhs)
	}
	err := un.visitMaybeNested(lhs, lhsParen)
	if err != nil {
		return err
	}
	unmangled, found := operators.FindReverseBinaryOperator(fun)
	if !found {
		return fmt.Errorf("cannot unmangle operator: %s", fun)
	}

	un.writeOperatorWithWrapping(fun, unmangled)
	return un.visitMaybeNested(rhs, rhsParen)
}

func (un *formatter) visitCallConditional(expr ast.Expr) error {
	c := expr.AsCall()
	args := c.Args()
	// add parens if operand is a conditional itself.
	nested := isSamePrecedence(operators.Conditional, args[0]) ||
		isComplexOperator(args[0])
	err := un.visitMaybeNested(args[0], nested)
	if err != nil {
		return err
	}
	if un.isMultiline(expr) {
		un.WriteString(" ?")
		un.indent++
		un.WriteString(un.Comment(args[0].ID()))
		un.WriteNewLine()

		// add parens if operand is a conditional itself.
		nested = isSamePrecedence(operators.Conditional, args[1]) ||
			isComplexOperator(args[1])
		err = un.visitMaybeNested(args[1], nested)
		if err != nil {
			return err
		}

		un.indent--
		un.WriteString(un.Comment(args[1].ID()))
		un.WriteNewLine()
		un.WriteString(":")
		cuddle := args[2].Kind() == ast.CallKind && args[2].AsCall().FunctionName() == operators.Conditional
		if cuddle {
			un.WriteString(" ")
		} else {
			un.indent++
			un.WriteNewLine()
		}

		err = un.visit(args[2], false)
		if !cuddle {
			un.indent--
		}
		return err
	}
	un.writeOperatorWithWrapping(operators.Conditional, "?")

	// add parens if operand is a conditional itself.
	nested = isSamePrecedence(operators.Conditional, args[1]) ||
		isComplexOperator(args[1])
	err = un.visitMaybeNested(args[1], nested)
	if err != nil {
		return err
	}

	un.WriteString(" : ")
	// add parens if operand is a conditional itself.
	nested = isSamePrecedence(operators.Conditional, args[2]) ||
		isComplexOperator(args[2])

	return un.visitMaybeNested(args[2], nested)
}

func (un *formatter) visitCallFunc(expr ast.Expr, macro bool) error {
	c := expr.AsCall()
	fun := c.FunctionName()
	args := c.Args()
	if c.IsMemberFunction() {
		nested := isBinaryOrTernaryOperator(c.Target())
		err := un.visitMaybeNested(c.Target(), nested)
		if err != nil {
			return err
		}
		un.WriteString(".")
	}
	if len(args) == 0 {
		un.WriteString(fun + "()")
		return nil
	}
	switch {
	case macro:
		un.WriteString(fun + "(")

		// get AST ID for macro. this is not stored in the
		// expression, so we need to do a scan of all macros.
		var id int64
		for candid, cand := range un.info.MacroCalls() {
			if expr == cand {
				id = candid
				break
			}
		}
		base := un.info.GetStartLocation(id).Line()
		var (
			last    int
			wasTern bool
		)
		for i, arg := range args {
			var line int
			lastLine := un.info.GetStartLocation(un.lastChild(arg).ID()).Line()
			if arg.Kind() == ast.CallKind && arg.AsCall().FunctionName() == operators.Conditional {
				line = un.info.GetStartLocation(arg.ID()).Line()
				if line != lastLine {
					wasTern = true
				}
			} else {
				line = lastLine
			}
			if line != base {
				break
			}
			last = i + 1
		}

		// write single-line section.
		for i, arg := range args[:last] {
			err := un.visit(arg, false)
			if err != nil {
				return err
			}
			if i < last-1 {
				un.WriteString(", ")
			}
		}
		if last != 0 && last < len(args) {
			un.WriteString(",")
		}
		if last == len(args) {
			if wasTern {
				un.WriteNewLine()
			}
			un.WriteString(")")
			return nil
		}

		// write multi-line section.
		un.indent++
		for i, arg := range args[last:] {
			un.WriteNewLine()
			err := un.visit(arg, false)
			if err != nil {
				return err
			}
			if i < len(args[last:])-1 {
				un.WriteString(",")
			}
		}
		un.indent--
		un.WriteNewLine()
		un.WriteString(")")
	case un.isMultiline(expr):
		un.WriteString(fun + "(")
		un.indent++
		for i, arg := range args {
			un.WriteNewLine()
			err := un.visit(arg, false)
			if err != nil {
				return err
			}
			if i < len(args)-1 {
				un.WriteString(",")
			}
		}
		un.indent--
		un.WriteNewLine()
		un.WriteString(")")
	default:
		un.WriteString(fun + "(")
		for i, arg := range args {
			err := un.visit(arg, false)
			if err != nil {
				return err
			}
			if i < len(args)-1 {
				un.WriteString(", ")
			}
		}
		un.WriteString(")")
	}
	return nil
}

func (un *formatter) visitCallIndex(expr ast.Expr) error {
	return un.visitCallIndexInternal(expr, "[")
}

func (un *formatter) visitCallOptIndex(expr ast.Expr) error {
	return un.visitCallIndexInternal(expr, "[?")
}

func (un *formatter) visitCallIndexInternal(expr ast.Expr, op string) error {
	c := expr.AsCall()
	args := c.Args()
	nested := isBinaryOrTernaryOperator(args[0])
	err := un.visitMaybeNested(args[0], nested)
	if err != nil {
		return err
	}
	un.WriteString(op)
	err = un.visit(args[1], false)
	if err != nil {
		return err
	}
	un.WriteString("]")
	return nil
}

func (un *formatter) visitCallUnary(expr ast.Expr) error {
	c := expr.AsCall()
	fun := c.FunctionName()
	args := c.Args()
	unmangled, found := operators.FindReverse(fun)
	if !found {
		return fmt.Errorf("cannot unmangle operator: %s", fun)
	}
	un.WriteString(unmangled)
	nested := isComplexOperator(args[0])
	return un.visitMaybeNested(args[0], nested)
}

func (un *formatter) visitConst(expr ast.Expr) error {
	val := expr.AsLiteral()
	switch val := val.(type) {
	case types.Bool:
		un.WriteString(strconv.FormatBool(bool(val)))
	case types.Bytes:
		// try to handle literal byte strings.
		if un.options.pretty && !bytes.ContainsFunc([]byte(val), func(r rune) bool {
			return !unicode.IsGraphic(r) && !unicode.IsSpace(r)
		}) {
			syn := un.syntax(expr.ID())
			if strings.EqualFold(syn, "b'''") || strings.EqualFold(syn, `b"""`) ||
				strings.EqualFold(syn, "br'''") || strings.EqualFold(syn, `br"""`) {
				un.WriteString(syn)
				un.WriteString(string(val))
				un.WriteString(strings.TrimLeft(syn, "bBrR"))
				break
			}
		}
		// otherwise bytes constants are surrounded with b"<bytes>"
		un.WriteString(`b"`)
		un.WriteString(bytesToOctets([]byte(val)))
		un.WriteString(`"`)
	case types.Double:
		// represent the float using the minimum required digits
		d := strconv.FormatFloat(float64(val), 'g', -1, 64)
		un.WriteString(d)
		if !strings.Contains(d, ".") {
			un.WriteString(".0")
		}
	case types.Int:
		i := strconv.FormatInt(int64(val), 10)
		un.WriteString(i)
	case types.Null:
		un.WriteString("null")
	case types.String:
		syn := un.syntax(expr.ID())
		if un.options.pretty && syn == "'''" || syn == `"""` ||
			strings.EqualFold(syn, "r'''") || strings.EqualFold(syn, `r"""`) {
			// handle literal strings.
			un.WriteString(syn)
			un.WriteString(string(val))
			un.WriteString(strings.TrimLeft(syn, "rR"))
		} else {
			// otherwise strings will be double quoted with quotes escaped.
			un.WriteString(strconv.Quote(string(val)))
		}
	case types.Uint:
		// uint literals have a 'u' suffix.
		ui := strconv.FormatUint(uint64(val), 10)
		un.WriteString(ui)
		un.WriteString("u")
	default:
		return fmt.Errorf("unsupported constant: %v", expr)
	}
	return nil
}

func (un *formatter) syntax(id int64) string {
	start := un.info.GetStartLocation(id)
	snippet, ok := un.src.Snippet(start.Line())
	if ok {
		if start.Column() > len(snippet) {
			return snippet
		}
		return snippet[start.Column():]
	}
	return ""
}

func (un *formatter) visitIdent(expr ast.Expr) error {
	un.WriteString(expr.AsIdent())
	return nil
}

func (un *formatter) visitList(expr ast.Expr) error {
	l := expr.AsList()
	elems := l.Elements()
	optIndices := make(map[int]bool, len(elems))
	for _, idx := range l.OptionalIndices() {
		optIndices[int(idx)] = true
	}
	un.WriteString("[")
	if un.isMultiline(expr) {
		un.indent++
		for i, elem := range elems {
			un.WriteNewLine()
			if optIndices[i] {
				for _, c := range un.CommentBlock(elem.ID()) {
					un.WriteString(c)
					un.WriteNewLine()
				}
				un.WriteString("?")
			}
			err := un.visit(elem, false)
			if err != nil {
				return err
			}
			if un.options.alwaysComma || i < len(elems)-1 {
				un.WriteString(",")
			}
			un.WriteString(un.Comment(un.lastChild(elem).ID()))
		}
		un.indent--
		un.WriteNewLine()
		un.WriteString("]")
	} else {
		for i, elem := range elems {
			if optIndices[i] {
				un.WriteString("?")
			}
			err := un.visit(elem, false)
			if err != nil {
				return err
			}
			if i < len(elems)-1 {
				un.WriteString(", ")
			}
		}
		un.WriteString("]")
	}
	return nil
}

func (un *formatter) visitOptSelect(expr ast.Expr) error {
	c := expr.AsCall()
	args := c.Args()
	operand := args[0]
	field := args[1].AsLiteral().(types.String)
	return un.visitSelectInternal(operand, false, ".?", string(field))
}

func (un *formatter) visitSelect(expr ast.Expr) error {
	sel := expr.AsSelect()
	return un.visitSelectInternal(sel.Operand(), sel.IsTestOnly(), ".", sel.FieldName())
}

func (un *formatter) visitSelectInternal(operand ast.Expr, testOnly bool, op, field string) error {
	// handle the case when the select expression was generated by the has() macro.
	if testOnly {
		un.WriteString("has(")
	}
	nested := !testOnly && isBinaryOrTernaryOperator(operand)
	err := un.visitMaybeNested(operand, nested)
	if err != nil {
		return err
	}
	un.WriteString(op)
	un.WriteString(field)
	if testOnly {
		un.WriteString(")")
	}
	return nil
}

func (un *formatter) visitStructMsg(expr ast.Expr) error {
	m := expr.AsStruct()
	fields := m.Fields()
	un.WriteString(m.TypeName())
	un.WriteString("{")
	if un.isMultiline(expr) {
		un.indent++
		for i, f := range fields {
			field := f.AsStructField()
			f := field.Name()
			v := field.Value()
			un.WriteNewLine()
			if field.IsOptional() {
				for _, c := range un.CommentBlock(v.ID()) {
					un.WriteString(c)
					un.WriteNewLine()
				}
				un.WriteString("?")
			}
			un.WriteString(f)
			un.WriteString(": ")
			err := un.visit(v, false)
			if err != nil {
				return err
			}
			if un.options.alwaysComma || i < len(fields)-1 {
				un.WriteString(",")
			}
			un.WriteString(un.Comment(un.lastChild(field.Value()).ID()))
		}
		un.indent--
		un.WriteNewLine()
		un.WriteString("}")
	} else {
		for i, f := range fields {
			field := f.AsStructField()
			f := field.Name()
			if field.IsOptional() {
				un.WriteString("?")
			}
			un.WriteString(f)
			un.WriteString(": ")
			v := field.Value()
			err := un.visit(v, false)
			if err != nil {
				return err
			}
			if i < len(fields)-1 {
				un.WriteString(", ")
			}
		}
		un.WriteString("}")
	}
	return nil
}

func (un *formatter) visitStructMap(expr ast.Expr) error {
	m := expr.AsMap()
	entries := m.Entries()
	un.WriteString("{")
	if un.isMultiline(expr) {
		un.indent++
		for i, e := range entries {
			entry := e.AsMapEntry()
			k := entry.Key()
			un.WriteNewLine()
			if entry.IsOptional() {
				for _, c := range un.CommentBlock(e.ID()) {
					un.WriteString(c)
					un.WriteNewLine()
				}
				un.WriteString("?")
			}
			err := un.visit(k, false)
			if err != nil {
				return err
			}
			un.WriteString(": ")
			v := entry.Value()
			err = un.visit(v, false)
			if err != nil {
				return err
			}
			if un.options.alwaysComma || i < len(entries)-1 {
				un.WriteString(",")
			}
			un.WriteString(un.Comment(un.lastChild(entry.Value()).ID()))
		}
		un.indent--
		un.WriteNewLine()
		un.WriteString("}")
	} else {
		for i, e := range entries {
			entry := e.AsMapEntry()
			k := entry.Key()
			if entry.IsOptional() {
				un.WriteString("?")
			}
			err := un.visit(k, false)
			if err != nil {
				return err
			}
			un.WriteString(": ")
			v := entry.Value()
			err = un.visit(v, false)
			if err != nil {
				return err
			}
			if i < len(entries)-1 {
				un.WriteString(", ")
			}
		}
		un.WriteString("}")
	}
	return nil
}

func (un *formatter) visitMaybeMacroCall(expr ast.Expr) (bool, error) {
	call, found := un.info.GetMacroCall(expr.ID())
	if !found {
		return false, nil
	}
	return true, un.visit(call, true)
}

func (un *formatter) visitMaybeNested(expr ast.Expr, nested bool) error {
	// Use multiline format if the expression spans multiple lines OR if it has
	// preceding comments (which would span multiple lines when written).
	// We check the entire expression tree for comments since comments may be
	// associated with descendant expressions.
	multiline := un.isMultiline(expr) || (nested && un.hasCommentsInTree(expr))

	if multiline {
		if nested {
			un.indent++
			un.WriteString("(")
			un.WriteNewLine()
		}
		err := un.visit(expr, false)
		if err != nil {
			return err
		}
		if nested {
			un.indent--
			un.WriteNewLine()
			un.WriteString(")")
		}
	} else {
		if nested {
			un.WriteString("(")
		}
		err := un.visit(expr, false)
		if err != nil {
			return err
		}
		if nested {
			un.WriteString(")")
		}
	}

	return nil
}

func (un *formatter) isMultiline(expr ast.Expr) bool {
	start := un.info.GetStartLocation(expr.ID())
	stop := un.info.GetStopLocation(un.lastChild(expr).ID())
	return start.Line() != stop.Line()
}

// hasCommentsInTree returns whether there are unclaimed comments preceding this
// expression or any of its descendant expressions. This is used to decide
// whether to use multiline format for nested expressions.
func (un *formatter) hasCommentsInTree(expr ast.Expr) bool {
	if !un.options.pretty {
		return false
	}
	return un.hasCommentsForExpr(expr.ID()) || un.hasCommentsInDescendants(expr)
}

// hasCommentsForExpr returns whether there are unclaimed comments directly
// preceding the expression with the given ID.
func (un *formatter) hasCommentsForExpr(id int64) bool {
	if !un.options.pretty {
		return false
	}
	start := un.info.GetStartLocation(id)
	first := true
	for line := start.Line(); line > 0; line-- {
		text, ok := un.src.Snippet(line)
		comment := strings.TrimSpace(text)
		if !ok || (comment != "" && !strings.HasPrefix(comment, "//")) {
			if first {
				first = false
				continue
			}
			break
		}
		first = false
		loc := location{line, strings.Index(text, comment)}
		if cid, ok := un.comments[loc]; ok {
			if cid != id {
				return false
			}
			break
		}
		if comment != "" {
			return true
		}
	}
	return false
}

// hasCommentsInDescendants returns whether any descendant expression has
// unclaimed comments.
func (un *formatter) hasCommentsInDescendants(expr ast.Expr) bool {
	switch expr.Kind() {
	case ast.CallKind:
		c := expr.AsCall()
		if c.IsMemberFunction() {
			if un.hasCommentsInTree(c.Target()) {
				return true
			}
		}
		for _, arg := range c.Args() {
			if un.hasCommentsInTree(arg) {
				return true
			}
		}
	case ast.ListKind:
		for _, elem := range expr.AsList().Elements() {
			if un.hasCommentsInTree(elem) {
				return true
			}
		}
	case ast.MapKind:
		for _, entry := range expr.AsMap().Entries() {
			e := entry.AsMapEntry()
			if un.hasCommentsInTree(e.Key()) || un.hasCommentsInTree(e.Value()) {
				return true
			}
		}
	case ast.SelectKind:
		if un.hasCommentsInTree(expr.AsSelect().Operand()) {
			return true
		}
	case ast.StructKind:
		for _, field := range expr.AsStruct().Fields() {
			if un.hasCommentsInTree(field.AsStructField().Value()) {
				return true
			}
		}
	}
	return false
}

func (un *formatter) lastChild(expr ast.Expr) ast.Expr {
	call, ok := un.info.GetMacroCall(expr.ID())
	if ok {
		return un.lastChild(call)
	}
	switch expr.Kind() {
	case ast.CallKind:
		args := expr.AsCall().Args()
		if len(args) == 0 {
			return expr
		}
		return un.lastChild(args[len(args)-1])
	case ast.ListKind:
		elems := expr.AsList().Elements()
		if len(elems) == 0 {
			return expr
		}
		return un.lastChild(elems[len(elems)-1])
	case ast.MapKind:
		entries := expr.AsMap().Entries()
		if len(entries) == 0 {
			return expr
		}
		return un.lastChild(entries[len(entries)-1].AsMapEntry().Value())
	case ast.SelectKind:
		return un.lastChild(expr.AsSelect().Operand())
	case ast.StructKind:
		fields := expr.AsStruct().Fields()
		if len(fields) == 0 {
			return expr
		}
		return un.lastChild(fields[len(fields)-1].AsStructField().Value())
	default:
		return expr
	}
}

// isLeftRecursive indicates whether the parser resolves the call in a left-recursive manner as
// this can have an effect of how parentheses affect the order of operations in the AST.
func isLeftRecursive(op string) bool {
	return op != operators.LogicalAnd && op != operators.LogicalOr
}

// isSamePrecedence indicates whether the precedence of the input operator is the same as the
// precedence of the (possible) operation represented in the input Expr.
//
// If the expr is not a Call, the result is false.
func isSamePrecedence(op string, expr ast.Expr) bool {
	if expr.Kind() != ast.CallKind {
		return false
	}
	c := expr.AsCall()
	other := c.FunctionName()
	return operators.Precedence(op) == operators.Precedence(other)
}

// isLowerPrecedence indicates whether the precedence of the input operator is lower precedence
// than the (possible) operation represented in the input Expr.
//
// If the expr is not a Call, the result is false.
func isLowerPrecedence(op string, expr ast.Expr) bool {
	c := expr.AsCall()
	other := c.FunctionName()
	return operators.Precedence(op) < operators.Precedence(other)
}

// Indicates whether the expr is a complex operator, i.e., a call expression
// with 2 or more arguments.
func isComplexOperator(expr ast.Expr) bool {
	if expr.Kind() == ast.CallKind && len(expr.AsCall().Args()) >= 2 {
		return true
	}
	return false
}

// Indicates whether it is a complex operation compared to another.
// expr is *not* considered complex if it is not a call expression or has
// less than two arguments, or if it has a higher precedence than op.
func isComplexOperatorWithRespectTo(op string, expr ast.Expr) bool {
	if expr.Kind() != ast.CallKind || len(expr.AsCall().Args()) < 2 {
		return false
	}
	return isLowerPrecedence(op, expr)
}

// Indicate whether this is a binary or ternary operator.
func isBinaryOrTernaryOperator(expr ast.Expr) bool {
	if expr.Kind() != ast.CallKind || len(expr.AsCall().Args()) < 2 {
		return false
	}
	_, isBinaryOp := operators.FindReverseBinaryOperator(expr.AsCall().FunctionName())
	return isBinaryOp || isSamePrecedence(operators.Conditional, expr)
}

// bytesToOctets converts byte sequences to a string using a three digit octal encoded value
// per byte.
func bytesToOctets(byteVal []byte) string {
	var b strings.Builder
	for _, c := range byteVal {
		fmt.Fprintf(&b, "\\%03o", c)
	}
	return b.String()
}

// writeOperatorWithWrapping outputs the operator and inserts a newline for operators configured
// in the unparser options.
func (un *formatter) writeOperatorWithWrapping(fun, unmangled string) bool {
	_, wrapOperatorExists := un.options.operatorsToWrapOn[fun]
	lineLength := un.dst.Len() - un.lastWrappedIndex + len(fun)

	if wrapOperatorExists && lineLength >= un.options.wrapOnColumn {
		un.lastWrappedIndex = un.dst.Len()
		// wrapAfterColumnLimit flag dictates whether the newline is placed
		// before or after the operator
		if un.options.wrapAfterColumnLimit {
			// Input: a && b
			// Output: a &&\nb
			un.WriteString(" ")
			un.WriteString(unmangled)
			if un.options.pretty {
				un.WriteNewLine()
			} else {
				un.WriteString("\n")
			}
		} else {
			// Input: a && b
			// Output: a\n&& b
			if un.options.pretty {
				un.WriteNewLine()
			} else {
				un.WriteString("\n")
			}
			un.WriteString(unmangled)
			un.WriteString(" ")
		}
		return true
	}
	un.WriteString(" ")
	un.WriteString(unmangled)
	un.WriteString(" ")
	return false
}

// Defined defaults for the unparser options
var (
	defaultWrapOnColumn         = 80
	defaultWrapAfterColumnLimit = true
	defaultIndentString         = "\t"
	defaultOperatorsToWrapOn    = map[string]bool{
		operators.LogicalAnd: true,
		operators.LogicalOr:  true,
	}
)

// FormatOption is a functional option for configuring the output formatting
// of the Unparse function.
type FormatOption func(*unparserOption) (*unparserOption, error)

// Internal representation of the UnparserOption type plus a sneaky output option.
type unparserOption struct {
	wrapOnColumn         int
	operatorsToWrapOn    map[string]bool
	wrapAfterColumnLimit bool
	pretty               bool
	alwaysComma          bool

	// indent is the string to be repeated for indented lines.
	indent string
}

// Pretty enables pretty printing of the output expression.
func Pretty() FormatOption {
	return func(opt *unparserOption) (*unparserOption, error) {
		opt.pretty = true
		return opt, nil
	}
}

// AlwaysComma forces a comma to be printed after the last element of a list or map.
func AlwaysComma() FormatOption {
	return func(opt *unparserOption) (*unparserOption, error) {
		opt.alwaysComma = true
		return opt, nil
	}
}

// IndentString sets the string to use to indent lines. If not set this defaults
// to "\t".
func IndentString(s string) FormatOption {
	return func(opt *unparserOption) (*unparserOption, error) {
		if s != "" {
			opt.indent = s
		}
		return opt, nil
	}
}

// WrapOnColumn wraps the output expression when its string length exceeds a specified limit
// for operators set by WrapOnOperators function or by default, "&&" and "||" will be wrapped.
//
// Example usage:
//
//	Unparse(expr, sourceInfo, WrapOnColumn(40), WrapOnOperators(Operators.LogicalAnd))
//
// This will insert a newline immediately after the logical AND operator for the below example input:
//
// Input:
// 'my-principal-group' in request.auth.claims && request.auth.claims.iat > now - duration('5m')
//
// Output:
// 'my-principal-group' in request.auth.claims &&
// request.auth.claims.iat > now - duration('5m')
func WrapOnColumn(col int) FormatOption {
	return func(opt *unparserOption) (*unparserOption, error) {
		if col < 1 {
			return nil, fmt.Errorf("Invalid unparser option. Wrap column value must be greater than or equal to 1. Got %v instead", col)
		}
		opt.wrapOnColumn = col
		return opt, nil
	}
}

// WrapOnOperators specifies which operators to perform word wrapping on an output expression when its string length
// exceeds the column limit set by WrapOnColumn function.
//
// Word wrapping is supported on non-unary symbolic operators. Refer to operators.go for the full list
//
// This will replace any previously supplied operators instead of merging them.
func WrapOnOperators(symbols ...string) FormatOption {
	return func(opt *unparserOption) (*unparserOption, error) {
		opt.operatorsToWrapOn = make(map[string]bool)
		for _, symbol := range symbols {
			_, found := operators.FindReverse(symbol)
			if !found {
				return nil, fmt.Errorf("Invalid unparser option. Unsupported operator: %s", symbol)
			}
			arity := operators.Arity(symbol)
			if arity < 2 {
				return nil, fmt.Errorf("Invalid unparser option. Unary operators are unsupported: %s", symbol)
			}

			opt.operatorsToWrapOn[symbol] = true
		}

		return opt, nil
	}
}

// WrapAfterColumnLimit dictates whether to insert a newline before or after the specified operator
// when word wrapping is performed.
//
// Example usage:
//
//	Unparse(expr, sourceInfo, WrapOnColumn(40), WrapOnOperators(Operators.LogicalAnd), WrapAfterColumnLimit(false))
//
// This will insert a newline immediately before the logical AND operator for the below example input, ensuring
// that the length of a line never exceeds the specified column limit:
//
// Input:
// 'my-principal-group' in request.auth.claims && request.auth.claims.iat > now - duration('5m')
//
// Output:
// 'my-principal-group' in request.auth.claims
// && request.auth.claims.iat > now - duration('5m')
func WrapAfterColumnLimit(wrapAfter bool) FormatOption {
	return func(opt *unparserOption) (*unparserOption, error) {
		opt.wrapAfterColumnLimit = wrapAfter
		return opt, nil
	}
}
