module github.com/mallardduck/BrambleGate/engine

go 1.26

toolchain go1.26.5

replace (
	github.com/mallardduck/BrambleGate/mdnssd => ../mdnssd
	github.com/mallardduck/BrambleGate/plugins/localrecords => ../plugins/localrecords
	github.com/mallardduck/BrambleGate/plugins/mdnsbridge => ../plugins/mdnsbridge
	github.com/mallardduck/BrambleGate/pluginreg => ../pluginreg
	github.com/mallardduck/BrambleGate/vlanmatch => ../vlanmatch
)

require (
	github.com/coredns/caddy v1.1.4
	github.com/coredns/coredns v1.14.6
	github.com/mallardduck/BrambleGate/plugins/localrecords v0.0.0-00010101000000-000000000000
	github.com/mallardduck/BrambleGate/plugins/mdnsbridge v0.0.0-00010101000000-000000000000
)

require (
	github.com/apparentlymart/go-cidr v1.1.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/dnstap/golang-dnstap v0.4.0 // indirect
	github.com/farsightsec/golang-framestream v0.3.0 // indirect
	github.com/flynn/go-shlex v0.0.0-20150515145356-3f9db97f8568 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-opentracing v0.0.0-20180507213350-8e809c8a8645 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/mallardduck/BrambleGate/mdnssd v0.0.0 // indirect
	github.com/mallardduck/BrambleGate/pluginreg v0.0.0 // indirect
	github.com/mallardduck/BrambleGate/vlanmatch v0.0.0 // indirect
	github.com/mdlayher/socket v0.6.1 // indirect
	github.com/mdlayher/vsock v1.3.0 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pires/go-proxyproto v0.15.0 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/exporter-toolkit v0.17.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.61.0 // indirect
	go.uber.org/automaxprocs v1.6.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260807164820-c8921c73eeea // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
