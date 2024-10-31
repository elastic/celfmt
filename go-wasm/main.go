// go: build wasm
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/elastic/celfmt"
	"github.com/elastic/mito/lib"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common"
)

type FormatReturn struct {
	Format string
	Err    error
}

func formatCelProgram(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return toObject(FormatReturn{"", fmt.Errorf("formatCelProgram requires one arg")})
	}
	celProgram := args[0].String()
	formatted := celFmt(celProgram)
	return toObject(formatted)
}

func celFmt(src string) FormatReturn {
	var buf bytes.Buffer
	fmt.Println("1")
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
		return FormatReturn{"", fmt.Errorf("failed to create env: %w", err)}
	}
	ast, iss := env.Compile(src)
	if iss != nil {
		return FormatReturn{"", fmt.Errorf("failed to parse program: %v", iss)}
	}
	err = celfmt.Format(&buf, ast.NativeRep(), common.NewTextSource(src), celfmt.Pretty(), celfmt.AlwaysComma())
	if err != nil {
		return FormatReturn{"", err}
	}
	return FormatReturn{buf.String(), nil}
}

func registerCallbacks() {
	js.Global().Set("formatCelProgram", js.FuncOf(formatCelProgram))
}

func main() {
	registerCallbacks()

	// Go runtime must be always available at any moment where exported functionality
	// can be executed, so keep it running till done.
	done := make(chan struct{})
	js.Global().Set("stopFormatCelProgram", js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		close(done)
		return nil
	}))
	<-done
}

// toObject converts a struct to a map[string]interface{} using JSON
// marshal/unmarshal.
func toObject(v interface{}) map[string]interface{} {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	var out map[string]interface{}
	if err = json.Unmarshal(data, &out); err != nil {
		panic(err)
	}

	return out
}
