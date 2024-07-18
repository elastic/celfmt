# `celfmt`

This contains a library function, `celfmt.Format` that can be used to canonically format CEL programs in a non-minified way and [a zero-configuration command](./cmd/celfmt) that uses that function to format CEL programs.

The command can be installed with `go install github.com/efd6/celfmt/cmd/celfmt@latest`.

`celfmt.Format` is forked from the original minifying formatter [here](https://pkg.go.dev/github.com/google/cel-go/parser#Unparse).