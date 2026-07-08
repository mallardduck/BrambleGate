module github.com/mallardduck/BrambleDNS/store

go 1.25

require (
	github.com/mallardduck/BrambleDNS/model v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

// Sibling library module resolved from disk (see docs/repo-layout.md).
replace github.com/mallardduck/BrambleDNS/model => ../model
