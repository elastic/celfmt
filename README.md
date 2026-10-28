# `celfmt`

This contains a library function, `celfmt.Format` that can be used to canonically format CEL programs in a non-minified way and [a zero-configuration command](./cmd/celfmt) that uses that function to format CEL programs.

The command can be installed with `go install github.com/elastic/celfmt/cmd/celfmt@latest`.

`celfmt.Format` is forked from the original minifying formatter [here](https://pkg.go.dev/github.com/google/cel-go/parser#Unparse).

The command may be used to format CEL programs in elastic agent integration configurations (`-agent`) or extract them (`-extract`). No option is needed to say which type of integration the template is for. The program is found in either of these places:

- `program:` at the start of a line (CEL input).
- `cel.program:` at the start of a line, or `program:` indented under a top-level `cel:` key (httpjson input). These are only looked for if the template has a top-level `run_as_cel: true`. Handlebars blocks and comments may appear between `cel:` and `program:`.

The output is always written with Unix line endings, even if the input has Windows line endings and has no program.

Limitations:

- The program MUST be a YAML block scalar (`|`, `|-`, `>`, ...). Other forms, such as `program: {{escape_string program}}`, are left unchanged.
- `run_as_cel` MUST be written as a literal `true`. A templated or conditional value is not detected, and `cel.program` is then left unchanged.
- The program MUST NOT contain handlebars statements.
- Only one program is formatted per template. If there are several, the one in the last content node (the text between handlebars statements) is used.
- `cel:` MUST be a top-level key, and text inside `{{#each}}`, `{{#unless}}` and other blocks except `{{#if}}` is not searched.

## License

This software is licensed under the Apache License, version 2 ("Apache-2.0").