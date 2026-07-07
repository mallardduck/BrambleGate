module github.com/mallardduck/BrambleDNS/configgen

go 1.25

require github.com/mallardduck/BrambleDNS/model v0.0.0

// Sibling library module resolved from disk (see docs/repo-layout.md).
replace github.com/mallardduck/BrambleDNS/model => ../model
