module github.com/mallardduck/BrambleDNS

go 1.25.0

require github.com/mallardduck/BrambleDNS/engine v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

require (
	github.com/apparentlymart/go-cidr v1.1.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coredns/caddy v1.1.4-0.20250930002214-15135a999495 // indirect
	github.com/coredns/coredns v1.14.4 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/dnstap/golang-dnstap v0.4.0 // indirect
	github.com/farsightsec/golang-framestream v0.3.0 // indirect
	github.com/flynn/go-shlex v0.0.0-20150515145356-3f9db97f8568 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-opentracing v0.0.0-20180507213350-8e809c8a8645 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mallardduck/BrambleDNS/configgen v0.0.0
	github.com/mallardduck/BrambleDNS/model v0.0.0 // indirect
	github.com/mallardduck/BrambleDNS/plugins/localrecords v0.0.0 // indirect
	github.com/mallardduck/BrambleDNS/store v0.0.0
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/mdlayher/vsock v1.2.1 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pires/go-proxyproto v0.12.0 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/exporter-toolkit v0.16.0 // indirect
	github.com/prometheus/procfs v0.19.2 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.1 // indirect
	go.uber.org/automaxprocs v1.6.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260511170946-3700d4141b60 // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// The root is the entrypoint binary and consumes the sibling library modules
// directly from disk. These replace directives make plain `go build`/`go install`
// and the Docker build work without publishing or tagging the sub-modules, and
// without relying on go.work being active. replace applies only to this main
// module, so it does not affect anyone who imports the sub-modules on their own.
replace github.com/mallardduck/BrambleDNS/engine => ./engine

replace github.com/mallardduck/BrambleDNS/configgen => ./configgen

replace github.com/mallardduck/BrambleDNS/store => ./store

replace github.com/mallardduck/BrambleDNS/model => ./model

replace github.com/mallardduck/BrambleDNS/plugins/localrecords => ./plugins/localrecords
