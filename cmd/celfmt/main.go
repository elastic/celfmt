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

// The celfmt command formats a CEL program in a canonical format.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/ext"
	"github.com/mailgun/raymond/v2/ast"
	"github.com/mailgun/raymond/v2/parser"
	"gopkg.in/yaml.v3"

	"github.com/elastic/mito/lib"

	"github.com/elastic/celfmt"
)

func main() {
	os.Exit(Main())
}

// Main is the entry point for the celfmt command. It formats a CEL program
// in a canonical format. It returns 0 on success and 1 on failure.
func Main() int {
	in := flag.String("i", "", "input file stdin if empty")
	out := flag.String("o", "", "output file stdout if empty")
	agent := flag.Bool("agent", false, "format agent config (incompatible with extract)")
	extract := flag.Bool("extract", false, "extract a formatted CEL program from an agent config (incompatible with agent)")
	simplify := flag.Bool("s", false, "simplify expressions")
	flag.Parse()

	if *agent && *extract {
		flag.Usage()
		return 1
	}

	var r io.Reader
	if *in == "" {
		r = os.Stdin
	} else {
		f, err := os.Open(*in)
		if err != nil {
			log.Printf("could not open input file: %v", err)
			return 1
		}
		defer f.Close()
		r = f
	}
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	if err != nil {
		log.Printf("could not read input: %v", err)
		return 1
	}

	var w io.Writer
	var outBuf bytes.Buffer
	if *out == "" {
		w = os.Stdout
	} else {
		w = &outBuf
	}

	if *agent || *extract {
		// Templates are always written out with Unix line endings.
		src := strings.ReplaceAll(buf.String(), "\r\n", "\n")
		ast, err := parser.Parse(src)
		if err != nil {
			panic(err)
		}
		var indent string
		if *agent {
			indent = "  "
		}
		v := &visitor{
			indent:   indent,
			simplify: *simplify,
			extract:  *extract,
			runAsCEL: runAsCELKey.MatchString(src),
		}
		ast.Accept(v)
		if v.err != nil {
			log.Printf("%v", v.err)
			return 1
		}
		if *agent {
			fmt.Fprint(w, strings.ReplaceAll(src, v.old, v.new))
		} else {
			fmt.Fprint(w, v.new)
		}
	} else {
		err = celFmt(w, buf.String(), "", *simplify)
		if err != nil {
			log.Printf("failed to format program: %v", err)
			return 1
		}
		fmt.Fprintln(w)
	}

	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			log.Printf("could not open output file: %v", err)
			return 1
		}
		defer func() {
			f.Sync()
			f.Close()
		}()
		if _, err = io.Copy(f, &outBuf); err != nil {
			log.Printf("could not write output: %v", err)
			return 1
		}
	}

	return 0
}

type visitor struct {
	old      string
	new      string
	indent   string
	simplify bool
	extract  bool
	err      error

	// runAsCEL is whether the template configures the httpjson input
	// to run as CEL, in which case the program is in cel.program.
	runAsCEL bool

	// seen is the text of all content nodes visited so far. It lets
	// findProgram look back past handlebars blocks to find the
	// parent key of an indented program field.
	seen strings.Builder
}

func (v *visitor) VisitProgram(node *ast.Program) any {
	for _, n := range node.Body {
		n.Accept(v)
	}
	return nil
}

func (v *visitor) VisitContent(s *ast.ContentStatement) any {
	defer v.seen.WriteString(s.Value)

	blk, ok := findProgram(v.seen.String(), s.Value, v.runAsCEL)
	if !ok {
		return nil
	}
	program, err := celFmtYAML(blk, v.indent, v.simplify, v.extract)
	if err != nil {
		if errors.As(err, &warn{}) {
			log.Printf("did not format program field content at line %d: %s", s.Line, err)
		}
		v.err = err
		return nil
	}
	v.old = s.Value
	if v.extract {
		v.new = program + "\n"
	} else {
		v.new = blk.prefix + program + blk.suffix
	}
	return nil
}

// programBlock is a block scalar program field found in a content node.
type programBlock struct {
	prefix string // Text before the field.
	body   string // The field, from its key to the end of its block scalar.
	suffix string // Text after the field.

	key string // Field key: "program" or "cel.program".
	col int    // Column of the key.
}

// runAsCELKey matches a top level run_as_cel: true in a template.
var runAsCELKey = regexp.MustCompile(`(?m)^run_as_cel:[ \t]+true[ \t]*(#.*)?$`)

// programKey matches the start of a block scalar program field.
var programKey = regexp.MustCompile(`(?m)^( *)(cel\.program|program):[ \t]+[|>][-+0-9]*([ \t]+#.*)?[ \t]*$`)

// findProgram looks for a program field in s, the text of a content
// node. The field is either a top level program (CEL input) or cel.program
// (httpjson input with run_as_cel), or an indented program that is a child
// of a top level cel key. The cel forms are only recognised if runAsCEL
// is true. before is the text that precedes s in the template, used to
// find the parent of an indented field.
func findProgram(before, s string, runAsCEL bool) (programBlock, bool) {
	for _, m := range programKey.FindAllStringSubmatchIndex(s, -1) {
		start := m[0]
		col := m[3] - m[2]
		key := s[m[4]:m[5]]
		switch {
		case key == "cel.program":
			if !runAsCEL {
				continue
			}
		case col != 0:
			if !runAsCEL || !underCEL(before+s[:start], col) {
				continue
			}
		}
		end := blockEnd(s, m[1], col)
		return programBlock{prefix: s[:start], body: s[start:end], suffix: s[end:], key: key, col: col}, true
	}
	return programBlock{}, false
}

// blockEnd returns the offset in s of the end of the block scalar whose key
// line ends at from and is at column col. Trailing blank lines are part of
// the block, so they are not preserved when the program is rewritten.
func blockEnd(s string, from, col int) int {
	pos := from
	if pos < len(s) {
		pos++ // Skip the newline ending the key line.
	}
	for pos < len(s) {
		next := len(s)
		if i := strings.IndexByte(s[pos:], '\n'); i >= 0 {
			next = pos + i + 1
		}
		line := s[pos:next]
		if strings.TrimSpace(line) != "" && leadingSpaces(line) <= col {
			break
		}
		pos = next
	}
	return pos
}

// underCEL returns whether the nearest preceding line in text that is
// indented less than col is the top level key "cel". Blank and comment
// lines are ignored. Handlebars statements are not part of text. The parser
// strips the whitespace around block, comment and partial statements that
// stand alone on a line, and a standalone mustache leaves a blank line,
// which is skipped.
func underCEL(text string, col int) bool {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || leadingSpaces(l) >= col {
			continue
		}
		key, rest, _ := strings.Cut(t, ":")
		rest = strings.TrimSpace(rest)
		return leadingSpaces(l) == 0 && key == "cel" && (rest == "" || strings.HasPrefix(rest, "#"))
	}
	return false
}

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

func (v *visitor) VisitBlock(s *ast.BlockStatement) any {
	p, ok := s.Expression.Path.(*ast.PathExpression)
	if !ok || p.Original != "if" {
		return nil
	}
	if s.Program != nil {
		for _, n := range s.Program.Body {
			n.Accept(v)
		}
	}
	if s.Inverse != nil {
		for _, n := range s.Inverse.Body {
			n.Accept(v)
		}
	}
	return nil
}

func celFmtYAML(blk programBlock, indent string, simplify, extract bool) (string, error) {
	var n yaml.Node
	err := yaml.Unmarshal([]byte(blk.body), &n)
	if err != nil {
		return "", err
	}
	if len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode || len(n.Content[0].Content) != 2 {
		return "", fmt.Errorf("unexpected shape")
	}

	var buf strings.Builder
	err = celFmt(&buf, n.Content[0].Content[1].Value, indent, simplify)
	if err != nil {
		return "", warn{err}
	}
	if extract {
		return buf.String(), nil
	}
	// We should be able to do this properly, but there is no
	// non-buggy YAML library that will not double-quote some
	// programs.
	pad := strings.Repeat(" ", blk.col)
	return pad + blk.key + ": |-\n" + pad + "  " + strings.ReplaceAll(buf.String(), "\n", "\n"+pad+"  ") + "\n", nil
}

type warn struct{ error }

func celFmt(dst io.Writer, src, indent string, simplify bool) error {
	xmlHelper, err := lib.XML(nil, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize xml helper: %w", err)
	}
	env, err := cel.NewEnv(
		cel.VariableDecls(
			decls.NewVariable("state", types.DynType),
			decls.NewVariable("useragent", types.StringType),
		),
		lib.Collections(),
		lib.Crypto(),
		lib.JSON(nil),
		lib.Time(),
		lib.Try(),
		lib.Debug(func(_ string, _ any) {}),
		lib.File(nil),
		lib.MIME(nil),
		lib.HTTP(nil, nil, nil),
		lib.Limit(nil),
		lib.Regexp(nil),
		lib.Strings(),
		lib.Printf(),
		xmlHelper,
		cel.OptionalTypes(cel.OptionalTypesVersion(lib.OptionalTypesVersion)),
		ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(lib.OptionalTypesVersion)),
		cel.EnableMacroCallTracking(),
		// Formatting adds whitespace, so already-formatted programs
		// can exceed the default limit even if the original did not.
		cel.ParserExpressionSizeLimit(-1),
	)
	if err != nil {
		return fmt.Errorf("failed to create env: %w", err)
	}
	compiled, iss := env.Compile(src)
	if iss != nil {
		return fmt.Errorf("failed to parse program: %v", iss)
	}
	textSrc := common.NewTextSource(src)
	if simplify {
		celfmt.Simplify(compiled.NativeRep(), textSrc)
	}
	opts := []celfmt.FormatOption{
		celfmt.Pretty(),
		celfmt.AlwaysComma(),
	}
	if indent != "" {
		opts = append(opts, celfmt.IndentString(indent))
	}
	return celfmt.Format(dst, compiled.NativeRep(), textSrc, opts...)
}

// ¯\_(ツ)_/¯
func (v *visitor) VisitMustache(*ast.MustacheStatement) any  { return nil }
func (v *visitor) VisitPartial(*ast.PartialStatement) any    { return nil }
func (v *visitor) VisitComment(*ast.CommentStatement) any    { return nil }
func (v *visitor) VisitExpression(*ast.Expression) any       { return nil }
func (v *visitor) VisitSubExpression(*ast.SubExpression) any { return nil }
func (v *visitor) VisitPath(*ast.PathExpression) any         { return nil }
func (v *visitor) VisitString(*ast.StringLiteral) any        { return nil }
func (v *visitor) VisitBoolean(*ast.BooleanLiteral) any      { return nil }
func (v *visitor) VisitNumber(*ast.NumberLiteral) any        { return nil }
func (v *visitor) VisitHash(*ast.Hash) any                   { return nil }
func (v *visitor) VisitHashPair(*ast.HashPair) any           { return nil }
