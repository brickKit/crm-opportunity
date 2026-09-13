module github.com/brickKit/crm-opportunity

go 1.25.0

require (
	github.com/brickKit/be-sdk-go v0.2.4
	github.com/brickKit/mdm-customer/gen/mdm/customer v1.0.6
	github.com/brickKit/mdm-product/gen/mdm/product v1.0.7
	github.com/gin-gonic/gin v1.12.0
	github.com/golang-migrate/migrate/v4 v4.19.1
	github.com/jackc/pgx/v5 v5.10.0
	github.com/nats-io/nats.go v1.53.1
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
)

// mdm-customer/gen/mdm/customer、mdm-product/gen/mdm/product 是各自组件
// 真身发布的生成物契约包（铁律六第二类白名单，设计书 §13.3），本组件
// 直接 import——不再逐字复制一份放进自己仓库的 contracts/vendor/（那个
// 目录已经清空删除）。
//
// 原因：本组件与 mdm-customer/mdm-product 虽然被分进了不同外壳
// （go-backoffice vs go-core，生产环境不会真的同进程），但 shells/go 的
// 测试文件把多个外壳的真实模块测试放进同一个 Go 测试二进制，编译阶段
// 依然会把两份内容相同的生成代码一起链进去，在 protobuf 全局注册表里
// 重复注册同一个文件/类型全名而 panic——Go 的 module system 无法把两个
// 不同 import path 的包合并成一份编译实例，replace 也解决不了（阶段四
// 调研记录 04 §13 有完整推演，含最小复现；erp-sales 已经据此完成同样的
// 改动）。

require (
	github.com/MicahParks/jwkset v0.11.3 // indirect
	github.com/MicahParks/keyfunc/v3 v3.8.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.46.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/sdk v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	golang.org/x/arch v0.22.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
