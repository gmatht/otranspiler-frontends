module github.com/gmatht/sh2loop/frontends/cpp-sh-go

go 1.21

require (
	github.com/gmatht/sh2loop/frontends/c-sh-go v0.0.0
	github.com/gmatht/sh2loop/frontends/shir-emit-go v0.0.0
)

replace github.com/gmatht/sh2loop/frontends/c-sh-go => ../c-sh-go

replace github.com/gmatht/sh2loop/frontends/shir-emit-go => ../shir-emit-go
