package api

// The Local API client, server dispatcher, test stub and the wrong-role base
// type are generated from local.go; run "go generate ./internal/api" after
// changing the interface.
//go:generate go run ./gen -in local.go -rpc rpc_gen.go -stub apitest/stub_gen.go -unimpl unimpl_gen.go
