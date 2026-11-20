// Package gen — корень сгенерированного gRPC-кода.
package gen

//go:generate sh -c "protoc -I../proto --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative ../proto/sync/v1/sync.proto"
