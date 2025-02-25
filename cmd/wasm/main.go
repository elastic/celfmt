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

//go:build js && wasm

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall/js"

	"github.com/elastic/mito/lib"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common"

	"github.com/elastic/celfmt"
)

//go:generate cp "$GOROOT/lib/wasm/wasm_exec.js" "$PWD/assets"

func compileAndFormat(dst io.Writer, src string) error {
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

type celFmtResult struct {
	Error     string `json:"error,omitempty"`
	Formatted string `json:"formatted,omitempty"`
}

// celFmt formats a given string using our CEL (Common Expression Language)
// formatting rules. It compiles the given program as part of the formatting
// process. This function takes one argument, which must be a string.
//
// The function always returns an object. On success, the object contains one
// attribute named 'formatted' which contains the formatted CEL program. If any
// error occurs, then the object contains an attribute named 'error' whose value
// is the string error message.
func celFmt(_ js.Value, args []js.Value) any {
	if len(args) != 1 {
		return toObject(&celFmtResult{Error: "celFmt requires one argument"})
	}
	if args[0].Type() != js.TypeString {
		return toObject(&celFmtResult{Error: "celFmt argument must be a string"})
	}

	buf := new(bytes.Buffer)
	if err := compileAndFormat(buf, args[0].String()); err != nil {
		return toObject(&celFmtResult{Error: err.Error()})
	}
	return toObject(&celFmtResult{Formatted: buf.String()})
}

// toObject converts a struct to a map[string]any using JSON marshal/unmarshal.
func toObject(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		panic(err)
	}

	return out
}

// moduleBuildMetadata returns a map containing build and dependency metadata
// about WebAssembly module. This includes version numbers for specific modules
// and other relevant settings such as commit hash and time.
//
// It then returns this map, which may contain the following keys:
//
//   - go: The Go language version used to build the program (without the "go" prefix).
//   - celfmt: The version of the main module in the build information.
//   - mito: The version of the "github.com/elastic/mito" module.
//   - cel-go: The version of the "github.com/google/cel-go" module.
//   - commit: The VCS revision (commit hash) from the build settings, if available.
//   - commit_time: The timestamp of the VCS revision from the build settings, if available.
func moduleBuildMetadata(_ js.Value, _ []js.Value) any {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	meta := map[string]any{
		"go":     strings.TrimPrefix(runtime.Version(), "go"),
		"celfmt": info.Main.Version,
	}

	for _, m := range info.Deps {
		switch m.Path {
		case "github.com/elastic/mito":
			meta["mito"] = m.Version
		case "github.com/google/cel-go":
			meta["cel-go"] = m.Version
		}
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			meta["commit"] = setting.Value
		case "vcs.time":
			meta["commit_time"] = setting.Value
		}
	}

	return meta
}

func main() {
	done := make(chan int, 0)
	js.Global().Set("celFmt", js.FuncOf(celFmt))
	js.Global().Set("celModuleBuildMetadata", js.FuncOf(moduleBuildMetadata))
	<-done
}
