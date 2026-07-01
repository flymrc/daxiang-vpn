package admin

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 --config=internal/spec/oapi-codegen.yaml internal/spec/openapi.yml
//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0 generate -f internal/db/sqlc.yaml
