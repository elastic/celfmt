const CEL_PROGRAM_DEFAULT_VALUE = `// Example CEL program
bytes(get(state.url).Body).as(body, {
  "events": [body.decode_json()]
})
`;

class CelFormatter {
    constructor(wasmUrl) {
        this.wasmUrl = wasmUrl;
        this.go = new globalThis.Go();
        this.init();
    }

    async init() {
        if (!WebAssembly.instantiateStreaming) {
            WebAssembly.instantiateStreaming = async (resp, importObject) => {
                const source = await (await resp).arrayBuffer();
                return await WebAssembly.instantiate(source, importObject);
            };
        }

        try {
            const result = await WebAssembly.instantiateStreaming(fetch(this.wasmUrl), this.go.importObject);
            this.go.run(result.instance);
            this.setVersionMetadata();
            this.setupUI();
        } catch (err) {
            console.error("Failed to load WebAssembly module:", err);
        }
    }

    setVersionMetadata() {
        const metadata = celModuleBuildMetadata();
        console.log("celfmt build metadata:", metadata);

        // Git commit.
        if (metadata.commit) {
            let a = document.getElementById("celftm-version-link");
            a.innerHTML = metadata.commit.substring(0, 7);
            a.href = "https://github.com/elastic/celfmt/commits/" + metadata.commit;
        }
        // elastic/mito release.
        if (metadata.mito) {
            let a = document.getElementById("mito-version-link");
            a.innerHTML = metadata.mito;
            a.href = "https://pkg.go.dev/github.com/elastic/mito@" + metadata.mito
        }
        // google/cel-go release.
        if (metadata["cel-go"]) {
            let a = document.getElementById("cel-go-version-link");
            a.innerHTML = metadata["cel-go"];
            a.href = "https://pkg.go.dev/github.com/google/cel-go@" + metadata["cel-go"];
        }
        // Go version.
        if (metadata.go) {
            let a = document.getElementById("go-version-link");
            a.innerHTML = metadata.go;
            a.href = "https://pkg.go.dev/std@go" + metadata.go
        }
    }

    setupUI() {
        const button = document.getElementById("button");
        if (button) {
            button.disabled = false;
            button.classList.add("enabled");
            button.onclick = () => this.applyCelFmt();
        }

        document.getElementById("input").value = CEL_PROGRAM_DEFAULT_VALUE;
    }

    applyCelFmt() {
        const inputSource = document.getElementById("input").value;

        const result = celFmt(inputSource);
        if (result.error) {
            document.getElementById('output').value = `‼️ ERROR\n${result.error}`;
        } else if (inputSource === result.formatted) {
            document.getElementById('output').value = "✅ CEL program is already formatted.";
        } else {
            document.getElementById('output').value = result.formatted;
        }
    }
}

window.celFormatter = new CelFormatter("celfmt.wasm");
