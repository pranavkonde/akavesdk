// Copyright (C) 2024 Akave
// See LICENSE for copying information.

// Package pb used to contain proto files and generated go files
package pb

//go:generate sh -c "protoc --proto_path=. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative $(ls *.proto)"
