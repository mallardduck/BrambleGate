module github.com/mallardduck/BrambleDNS/store

go 1.25

require (
	github.com/mallardduck/BrambleDNS/model v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

// Sibling library module resolved from disk (see docs/repo-layout.md).
replace github.com/mallardduck/BrambleDNS/model => ../model
