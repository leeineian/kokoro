module github.com/leeineian/kokoro

go 1.27.1

require (
	github.com/corentings/chess/v2 v2.6.0
	github.com/disgoorg/disgo v0.19.6
	github.com/disgoorg/godave v0.3.0
	github.com/disgoorg/omit v1.0.0
	github.com/disgoorg/snowflake/v2 v2.0.3
	github.com/ebitengine/purego v0.9.1
	github.com/fatih/color v1.19.0
	github.com/joho/godotenv v1.5.1
	github.com/lrstanley/go-ytdlp v1.5.4
	github.com/obinnaokechukwu/ffgo v0.1.1-0.20260801062543-8e9c9c1956ff
	github.com/ppalone/ytsearch v0.0.1
	github.com/raitonoberu/ytmusic v0.0.0-20240324143733-0e5780514b1d
	github.com/sho0pi/naturaltime v0.0.2
	golang.org/x/time v0.16.0
	google.golang.org/genai v1.71.0
	gosqlite.org v0.14.0
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.23.3 // indirect
	cloud.google.com/go/compute/metadata v0.9.1 // indirect
	github.com/FlameInTheDark/go-dave v1.1.1-0.20260522120015-58c6ae33f20c // indirect
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudflare/circl v1.6.5 // indirect
	github.com/disgoorg/json/v2 v2.0.0 // indirect
	github.com/dlclark/regexp2/v2 v2.8.0 // indirect
	github.com/dop251/goja v0.0.0-20260917113740-793a2a65c13b // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/pprof v0.0.0-20260906184651-6331bc6350fe // indirect
	github.com/google/s2a-go v0.1.10 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.22 // indirect
	github.com/googleapis/gax-go/v2 v2.25.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sasha-s/go-csync v0.0.0-20240107134140-fcbab37b09ad // indirect
	github.com/thomas-vilte/mls-go v1.0.0 // indirect
	github.com/ulikunitz/xz v0.5.17 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/api v0.299.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260921155816-b14227669459 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.54.0 // indirect
)

replace github.com/disgoorg/godave => ./.tmp/godave-pure

replace github.com/obinnaokechukwu/ffgo => ./.tmp/ffgo-local
