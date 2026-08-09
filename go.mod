module github.com/mallardduck/BrambleGate

go 1.26

toolchain go1.26.5

// The root is the entrypoint binary and consumes the sibling library modules
// directly from disk. These replace directives make plain `go build`/`go install`
// and the Docker build work without publishing or tagging the sub-modules, and
// without relying on go.work being active. replace applies only to this main
// module, so it does not affect anyone who imports the sub-modules on their own.

replace (
	github.com/mallardduck/BrambleGate/configgen => ./configgen
	github.com/mallardduck/BrambleGate/engine => ./engine
	github.com/mallardduck/BrambleGate/mdnssd => ./mdnssd
	github.com/mallardduck/BrambleGate/model => ./model
	github.com/mallardduck/BrambleGate/plugins/localrecords => ./plugins/localrecords
	github.com/mallardduck/BrambleGate/plugins/mdnsbridge => ./plugins/mdnsbridge
	github.com/mallardduck/BrambleGate/store => ./store
)

require (
	github.com/a-h/templ v0.3.1020
	github.com/brutella/dnssd v1.2.14
	github.com/go-acme/lego/v4 v4.35.2
	github.com/go-chi/chi/v5 v5.3.1
	github.com/mallardduck/BrambleGate/engine v0.0.0
)

require (
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.21.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns v1.2.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns v1.3.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph v0.9.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.6.0 // indirect
	github.com/a-h/parse v0.0.0-20250122154542-74294addb73e // indirect
	github.com/alexbrainman/sspi v0.0.0-20180613141037-e580b900e9f5 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.42.0 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.26 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.25 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/route53 v1.63.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.4 // indirect
	github.com/aws/smithy-go v1.27.1 // indirect
	github.com/bodgit/tsig v1.2.2 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cli/browser v1.3.0 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-resty/resty/v2 v2.17.2 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.15 // indirect
	github.com/googleapis/gax-go/v2 v2.22.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/goidentity/v6 v6.0.1 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jinzhu/copier v0.4.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/linode/linodego v1.68.0 // indirect
	github.com/mallardduck/BrambleGate/mdnssd v0.0.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.21 // indirect
	github.com/natefinch/atomic v1.0.1 // indirect
	github.com/openshift/gssapi v0.0.0-20161010215902-5fb4217df13b // indirect
	github.com/ovh/go-ovh v1.9.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/vishvananda/netlink v1.3.1 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.67.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	google.golang.org/api v0.280.0 // indirect
	gopkg.in/ini.v1 v1.67.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/apparentlymart/go-cidr v1.1.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coredns/caddy v1.1.4 // indirect
	github.com/coredns/coredns v1.14.6 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/dnstap/golang-dnstap v0.4.0 // indirect
	github.com/farsightsec/golang-framestream v0.3.0 // indirect
	github.com/flynn/go-shlex v0.0.0-20150515145356-3f9db97f8568 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-opentracing v0.0.0-20180507213350-8e809c8a8645 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/mallardduck/BrambleGate/configgen v0.0.0
	github.com/mallardduck/BrambleGate/model v0.0.0
	github.com/mallardduck/BrambleGate/plugins/localrecords v0.0.0 // indirect
	github.com/mallardduck/BrambleGate/plugins/mdnsbridge v0.0.0
	github.com/mallardduck/BrambleGate/store v0.0.0
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

tool github.com/a-h/templ/cmd/templ
