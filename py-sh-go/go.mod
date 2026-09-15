module github.com/gmatht/sh2loop/frontends/py-sh-go

go 1.22

toolchain go1.22.2

require (
	github.com/antlr4-go/antlr/v4 v4.13.1
	github.com/gmatht/sh2loop/frontends/shir-emit-go v0.0.0
)

require golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect

replace github.com/gmatht/sh2loop/frontends/shir-emit-go => ../shir-emit-go
