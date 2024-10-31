## Build

Run this from the root of the project.
```console
make build-wasm-with-go
```

If you are using Tinygo

```
make build-wasm-with-tinygo
```

- Copy the generated `celformat.wasm` file into the JS project.
- Run `cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" .` which copies the `wasm_exec.js` file in GOROOT to current directory. Copy this file into the JS project.

The WASM code can be executed in JS as in this example:

```typescript
async function loadWasm(input: string): Promise<string> {
  const goWasm = new Go();
  let value;
  try {
    const result = await WebAssembly.instantiate(file, goWasm.importObject);
    goWasm.run(result.instance);
    value = global.formatCelProgram(input);

    if (value === undefined) {
      throw new Error('Failed to format CEL program');
    }
    if(value.Err) {
      throw new Error(value.Err);
    }
  } finally {
    global.stopFormatCelProgram();
  }
  return JSON.stringify(value.Format);
}
```