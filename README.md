# `celfmt`

This contains a library function, `celfmt.Format` that can be used to canonically format CEL programs in a non-minified way and [a zero-configuration command](./cmd/celfmt) that uses that function to format CEL programs.

The command can be installed with `go install github.com/elastic/celfmt/cmd/celfmt@latest`.

`celfmt.Format` is forked from the original minifying formatter [here](https://pkg.go.dev/github.com/google/cel-go/parser#Unparse).

The command may be used to format CEL programs in elastic agent integration configurations with some limitations. In particular, CEL programs MUST be included in YAML pipe string literals with the field name `program` starting from the first column of the line.

## License

This software is licensed under the Apache License, version 2 ("Apache-2.0").