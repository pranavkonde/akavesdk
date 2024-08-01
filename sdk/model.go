// Copyright (C) 2024 Akave
// See LICENSE for copying information.

// Package sdk is the Akave SDK.
package sdk

import "time"

// BucketCreateResult is the result of bucket creation.
type BucketCreateResult struct {
	ID        string
	CreatedAt time.Time
}

// Bucket is a bucket.
type Bucket struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// FileChunk is a piece of metadata of some file.
type FileChunk struct {
	CID         string
	Data        []byte
	Size        uint64
	Permit      string
	NodeAddress string
	NodeID      string
}

// FileUpload represents a file and some metadata.
type FileUpload struct {
	RootCID    string
	BucketName string
	FileName   string
	FileSize   int64
	Chunks     []FileChunk
}

// FileDownload represents a file download and some metadata.
type FileDownload struct {
	Chunks []FileChunk
}

// FileListItem contains bucket file list file meta information.
type FileListItem struct {
	RootCID   string
	Name      string
	Size      int64
	CreatedAt time.Time
}

// FileMeta contains single file meta information.
type FileMeta struct {
	RootCID   string
	Name      string
	Size      int64
	CreatedAt time.Time
}
