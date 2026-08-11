module github.com/gmatht/sh2loop/frontends/cpp-sh-go

go 1.21

require (
	github.com/gmatht/sh2loop/frontends/c-sh-go v0.0.0
	github.com/gmatht/sh2loop/frontends/shir-emit-go v0.0.0
)

require github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82 // indirect

replace github.com/gmatht/sh2loop/frontends/c-sh-go => ../c-sh-go

replace github.com/gmatht/sh2loop/frontends/shir-emit-go => ../shir-emit-go
