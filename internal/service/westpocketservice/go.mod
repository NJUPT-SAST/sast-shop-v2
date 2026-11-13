module github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice

go 1.26.3

require (
	buf.build/gen/go/sast/sast-shop-v2/connectrpc/go v1.21.0-20261031123048-3d03e5d0dc6c.1
	buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go v1.36.12-20261031123048-3d03e5d0dc6c.2
	connectrpc.com/connect v1.21.1
	github.com/NJUPT-SAST/sast-shop-v2/internal/pkg v0.0.0
	github.com/disintegration/imaging v1.6.2
	github.com/labstack/echo/v5 v5.4.4
	github.com/larksuite/oapi-sdk-go/v3 v3.12.2
	github.com/rs/zerolog v1.35.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common v1.3.185
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/iai v1.3.155
	github.com/tencentyun/cos-go-sdk-v5 v0.7.76
	github.com/uptrace/bun v1.3.0
	github.com/uptrace/bun/dialect/pgdialect v1.3.0
	github.com/uptrace/bun/driver/pgdriver v1.3.0
	golang.org/x/image v0.48.0
	google.golang.org/protobuf v1.36.12
)

replace github.com/NJUPT-SAST/sast-shop-v2/internal/pkg => ../../pkg

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	buf.build/go/protovalidate v1.4.0 // indirect
	cel.dev/cel-go v0.32.0 // indirect
	cel.dev/expr v0.25.3 // indirect
	connectrpc.com/connect/v2 v2.0.0 // indirect
	connectrpc.com/validate v0.9.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/caarlos0/env/v11 v11.4.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clbanning/mxj v1.8.4 // indirect
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mitchellh/mapstructure v1.4.3 // indirect
	github.com/mozillazg/go-httpheader v0.2.1 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/redis/go-redis/v9 v9.23.0 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.opentelemetry.io/otel v1.41.0 // indirect
	go.opentelemetry.io/otel/trace v1.41.0 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
	mellium.im/sasl v0.3.2 // indirect
)
