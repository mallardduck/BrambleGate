module github.com/mallardduck/BrambleGate/selfip

go 1.26

toolchain go1.26.5

// Sibling library modules resolved from disk (see docs/repo-layout.md).
replace (
	github.com/mallardduck/BrambleGate/model => ../model
	github.com/mallardduck/BrambleGate/vlanmatch => ../vlanmatch
)

require (
	github.com/mallardduck/BrambleGate/model v0.0.0
	github.com/mallardduck/BrambleGate/vlanmatch v0.0.0
)