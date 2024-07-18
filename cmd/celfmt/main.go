// The celfmt command formats a CEL program in a canonical format.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/efd6/celfmt"
	"github.com/elastic/mito/lib"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common"
)

func main() {
	os.Exit(Main())
}

func Main() int {
	in := flag.String("i", "", "input file stdin if empty")
	out := flag.String("o", "", "output file stdout if empty")
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
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		cel.EnableMacroCallTracking(),
	)
	if err != nil {
		log.Printf("failed to create env: %v", err)
		return 1
	}

	ast, iss := env.Compile(buf.String())
	if iss != nil {
		log.Printf("failed to parse program: %v", iss)
		return 1
	}
	src := common.NewTextSource(buf.String())
	err = celfmt.Format(w, ast.NativeRep(), src, celfmt.Pretty(), celfmt.AlwaysComma())
	if err != nil {
		log.Printf("failed to format program: %v", err)
		return 1
	}
	fmt.Println()
	return 0
}
