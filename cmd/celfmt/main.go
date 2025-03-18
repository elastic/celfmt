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
	"strings"

	"github.com/elastic/celfmt"
	"github.com/elastic/mito/lib"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common"
	"github.com/mailgun/raymond/v2/ast"
	"github.com/mailgun/raymond/v2/parser"
	"gopkg.in/yaml.v3"
)

func main() {
	os.Exit(Main())
}

func Main() int {
	in := flag.String("i", "", "input file stdin if empty")
	out := flag.String("o", "", "output file stdout if empty")
	agent := flag.Bool("agent", false, "format agent config")
	flag.Parse()

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
	if *out == "" {
		w = os.Stdout
	} else {
		f, err := os.Create(*out)
		if err != nil {
			log.Printf("could not open output file: %v", err)
			return 1
		}
		defer func() {
			f.Sync()
			f.Close()
		}()
		w = f
	}

	if !*agent {
		err = celFmt(w, buf.String())
		if err != nil {
			log.Printf("failed to format program: %v", err)
			return 1
		}
		fmt.Fprintln(w)
	} else {
		ast, err := parser.Parse(buf.String())
		if err != nil {
			panic(err)
		}
		v := &visitor{}
		ast.Accept(v)
		if v.err != nil {
			log.Fatal(v.err)
		}
		fmt.Fprint(w, strings.ReplaceAll(buf.String(), v.old, v.new))
	}

	return 0
}

type visitor struct {
	old string
	new string
	err error
}

func (v *visitor) VisitProgram(node *ast.Program) any {
	for _, n := range node.Body {
		n.Accept(v)
	}
	return nil
}

func (v *visitor) VisitContent(s *ast.ContentStatement) any {
	prefix, program, suffix, err := findProgramYAML(s.Value)
	if err != nil {
		v.err = err
		return nil
	}
	if program != "" {
		program, err = celFmtYAML(program)
		if err != nil {
			if errors.As(err, &warn{}) {
				log.Printf("did not format program field content at line %d: %s", s.Line, err)
				return nil
			}
			v.err = err
			return nil
		}
		v.old = s.Value
		v.new = prefix + program + suffix
	}
	return nil
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

func celFmtYAML(src string) (string, error) {
	var n yaml.Node
	err := yaml.Unmarshal([]byte(src), &n)
	if err != nil {
		return "", err
	}
	if len(n.Content) != 1 && len(n.Content[0].Content) != 2 {
		return "", fmt.Errorf("unexpected shape")
	}

	var buf strings.Builder
	err = celFmt(&buf, n.Content[0].Content[1].Value)
	if err != nil {
		return "", warn{err}
	}
	// We should be able to do this properly, but there is no
	// non-buggy YAML library that will not double-quote some
	// programs.
	return "program: |-\n  " + strings.ReplaceAll(buf.String(), "\n", "\n  ") + "\n", nil
}

type warn struct{ error }

func celFmt(dst io.Writer, src string) error {
	xmlHelper, err := lib.XML(nil, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize xml helper: %w", err)
	}
	env, err := cel.NewEnv(
		cel.Declarations(decls.NewVar("state", decls.Dyn)),
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
		lib.Strings(),
		xmlHelper,
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		cel.EnableMacroCallTracking(),
	)
	if err != nil {
		return fmt.Errorf("failed to create env: %w", err)
	}
	ast, iss := env.Compile(src)
	if iss != nil {
		return fmt.Errorf("failed to parse program: %v", iss)
	}
	return celfmt.Format(dst, ast.NativeRep(), common.NewTextSource(src), celfmt.Pretty(), celfmt.AlwaysComma())
}

func findProgramYAML(s string) (prefix, program, suffix string, err error) {
	var yn yaml.Node
	idx := strings.Index(s, "\nprogram: |")
	if idx < 0 {
		if !strings.HasPrefix(s, "program: |") {
			return
		}
		// idx is -1 so the inc that follows
		// brings us to the start of the string.
	}
	idx++
	prefix = s[:idx]
	program = s[idx:]
	err = yaml.Unmarshal([]byte(program), &yn)
	if err != nil {
		return "", "", "", err
	}
	next := findNext(&yn, "program")
	if next == nil {
		return prefix, program, "", nil
	}
	suffix = s[idx:]
	for l := 1; l < next.Line; l++ {
		var ok bool
		_, suffix, ok = strings.Cut(suffix, "\n")
		if !ok {
			break
		}
	}
	program = strings.TrimSuffix(program, suffix)
	return prefix, program, suffix, nil
}

func findNext(node *yaml.Node, tag string) *yaml.Node {
	var keyOK, valOK bool
	for _, n := range node.Content {
		c := findNext(n, tag)
		if c != nil {
			return c
		}
		if valOK {
			return n
		}
		if keyOK {
			valOK = true
			continue
		}
		if n.Value == tag {
			keyOK = true
		}
	}
	return nil
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
