module github.com/mallardduck/BrambleGate/configgen

go 1.26

toolchain go1.26.5

// Sibling library module resolved from disk (see docs/repo-layout.md).
replace github.com/mallardduck/BrambleGate/model => ../model

require github.com/mallardduck/BrambleGate/model v0.0.0
