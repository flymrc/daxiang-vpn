package admin

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 --config=oapi-codegen.yaml openapi.yml
//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0 generate -f sqlc.yaml
