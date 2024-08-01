// Copyright (C) 2024 Akave
// See LICENSE for copying information.

package sdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"

	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-unixfs"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs/v2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"akave.ai/akavesdk/private/memory"
	"akave.ai/akavesdk/private/pb"
)

const (
	minBucketNameLength = 3
	chunkSize           = 1 * memory.MB // 1MB
	minFileSize         = 127           // 127 bytes
)

var errSDK = errs.Tag("sdk")
var mon = monkit.Package()

// SDK is the Akave SDK.
type SDK struct {
	client            pb.NodeAPIClient
	conn              *grpc.ClientConn
	maxConcurrency    int
	chunkSegmentSize  int64
	useConnectionPool bool
}

// New returns a new SDK.
func New(address string, maxConcurrency int, chunkSegmentSize int64, useConnectionPool bool) (*SDK, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &SDK{
		client:            pb.NewNodeAPIClient(conn),
		conn:              conn,
		maxConcurrency:    maxConcurrency,
		chunkSegmentSize:  chunkSegmentSize,
		useConnectionPool: useConnectionPool,
	}, nil
}

// Close closes the SDK internal connection.
func (sdk *SDK) Close() error {
	return sdk.conn.Close()
}

// CreateBucket creates a new bucket.
func (sdk *SDK) CreateBucket(ctx context.Context, name string) (_ *BucketCreateResult, err error) {
	defer mon.Task()(&ctx, name)(&err)

	if len(name) < minBucketNameLength {
		return nil, errSDK.Errorf("invalid bucket name")
	}

	res, err := sdk.client.BucketCreate(ctx, &pb.BucketCreateRequest{Name: name})
	if err != nil {
		return nil, errSDK.Wrap(err)
	}

	return &BucketCreateResult{
		ID:        res.Id,
		CreatedAt: res.CreatedAt.AsTime(),
	}, nil
}

// ViewBucket creates a new bucket.
func (sdk *SDK) ViewBucket(ctx context.Context, bucketName string) (_ Bucket, err error) {
	defer mon.Task()(&ctx, bucketName)(&err)

	if bucketName == "" {
		return Bucket{}, errSDK.Errorf("empty bucket name")
	}

	res, err := sdk.client.BucketView(ctx, &pb.BucketViewRequest{
		BucketName: bucketName,
	})

	return Bucket{
		ID:        res.GetId(),
		Name:      res.GetName(),
		CreatedAt: res.GetCreatedAt().AsTime(),
	}, nil
}

// ListBuckets returns list of buckets.
func (sdk *SDK) ListBuckets(ctx context.Context) (_ []Bucket, err error) {
	defer mon.Task()(&ctx)(&err)

	res, err := sdk.client.BucketList(ctx, &pb.BucketListRequest{})
	if err != nil {
		return nil, errSDK.Wrap(err)
	}

	buckets := make([]Bucket, 0, len(res.Buckets))
	for _, bucket := range res.Buckets {
		buckets = append(buckets, Bucket{
			ID:        bucket.GetId(),
			Name:      bucket.GetName(),
			CreatedAt: bucket.GetCreatedAt().AsTime(),
		})
	}

	return buckets, nil
}

// ListFiles returns list of files in a particular bucket.
func (sdk *SDK) ListFiles(ctx context.Context, bucketName string) (_ []FileListItem, err error) {
	defer mon.Task()(&ctx, bucketName)(&err)

	if bucketName == "" {
		return nil, errSDK.Errorf("empty bucket name")
	}

	resp, err := sdk.client.FileList(ctx, &pb.FileListRequest{
		BucketName: bucketName,
	})

	files := make([]FileListItem, 0, len(resp.List))
	for _, fileMeta := range resp.List {
		files = append(files, FileListItem{
			RootCID:   fileMeta.GetRootCid(),
			Name:      fileMeta.GetName(),
			Size:      fileMeta.GetSize(),
			CreatedAt: fileMeta.GetCreatedAt().AsTime(),
		})
	}

	return files, nil
}

// FileInfo returns meta information for single file by bucket and file name.
func (sdk *SDK) FileInfo(ctx context.Context, bucketName string, fileName string) (_ FileMeta, err error) {
	defer mon.Task()(&ctx, bucketName, fileName)(&err)
	if bucketName == "" {
		return FileMeta{}, errSDK.Errorf("empty bucket name")
	}
	if fileName == "" {
		return FileMeta{}, errSDK.Errorf("empty file name")
	}

	res, err := sdk.client.FileView(ctx, &pb.FileViewRequest{
		BucketName: bucketName,
		FileName:   fileName,
	})
	if err != nil {
		return FileMeta{}, errSDK.Wrap(err)
	}

	return FileMeta{
		RootCID:   res.GetRootCid(),
		Name:      res.GetName(),
		Size:      res.GetSize(),
		CreatedAt: res.CreatedAt.AsTime(),
	}, nil
}

// CreateFileUpload creates a new file upload request.
func (sdk *SDK) CreateFileUpload(ctx context.Context, bucketName string, fileName string, fileSize int64, reader io.Reader) (_ FileUpload, err error) {
	defer mon.Task()(&ctx, bucketName, fileName, fileSize)(&err)
	if bucketName == "" {
		return FileUpload{}, errSDK.Errorf("empty bucket name")
	}
	if fileSize < minFileSize {
		return FileUpload{}, errSDK.Errorf("file size is too small")
	}

	dag, err := CalculateDAG(ctx, reader, chunkSize.ToInt64())
	if err != nil {
		return FileUpload{}, err
	}

	rootCID := dag.RootCID.String()
	req := &pb.FileUploadCreateRequest{
		BucketName: bucketName,
		RootCid:    rootCID,
		Name:       fileName,
		Size:       fileSize,
	}
	req.Chunks = make([]*pb.FileUploadCreateRequest_Chunk, len(dag.Chunks))
	for i, chunk := range dag.Chunks {
		req.Chunks[i] = &pb.FileUploadCreateRequest_Chunk{
			Cid:  chunk.CID,
			Size: chunk.Size,
		}
	}

	res, err := sdk.client.FileUploadCreate(ctx, req)
	if err != nil {
		return FileUpload{}, errSDK.Wrap(err)
	}

	chunks := make([]FileChunk, len(res.ChunkUploads))
	for i, upload := range res.ChunkUploads {
		ch, found := chunkByCID(dag.Chunks, upload.Cid)
		if !found {
			return FileUpload{}, errSDK.Errorf("chunk not found")
		}
		chunks[i] = FileChunk{
			CID:         upload.Cid,
			Size:        upload.Size,
			NodeID:      upload.NodeId,
			NodeAddress: upload.NodeAddress,
			Permit:      upload.Permit,
			Data:        ch.Data,
		}
	}

	return FileUpload{
		RootCID:    rootCID,
		BucketName: bucketName,
		FileName:   fileName,
		FileSize:   fileSize,
		Chunks:     chunks,
	}, nil
}

// Upload uploads a file.
func (sdk *SDK) Upload(ctx context.Context, fileUpload FileUpload) (err error) {
	defer mon.Task()(&ctx, fileUpload)(&err)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(sdk.maxConcurrency)

	pool := newConnectionPool()
	defer func() {
		if err := pool.close(); err != nil {
			slog.Warn("failed to close connection", err)
		}
	}()

	for i, chunk := range fileUpload.Chunks {
		i, chunk := i, chunk
		deriveCtx := context.WithoutCancel(ctx)
		g.Go(func() (err error) {
			defer mon.Task()(&deriveCtx, chunk.CID)(&err)

			client, closer, err := pool.createClient(chunk.NodeAddress, sdk.useConnectionPool)
			if err != nil {
				return err
			}
			if closer != nil {
				defer func() {
					if closeErr := closer(); closeErr != nil {
						slog.Warn("failed to close connection", slog.Int("num", i), chunk.CID, chunk.NodeAddress, closeErr)
					}
				}()
			}

			sender, err := client.FileUploadChunk(ctx)
			if err != nil {
				return err
			}

			defer func() {
				if _, closeErr := sender.CloseAndRecv(); closeErr != nil {
					slog.Warn("failed to close and recv", slog.Int("num", i), chunk.CID, chunk.NodeAddress, closeErr)
				}
			}()

			err = uploadChunkSegments(ctx, &pb.FileChunkData{
				Data:     chunk.Data,
				ChunkCid: chunk.CID,
			}, sdk.chunkSegmentSize, sender.Send)
			if err != nil {
				return err
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errSDK.Wrap(err)
	}

	return nil
}

func uploadChunkSegments(ctx context.Context, chunk *pb.FileChunkData, chunkSegmentSize int64, sender func(*pb.FileChunkData) error) (err error) {
	defer mon.Task()(&ctx, chunkSegmentSize)(&err)
	if chunkSegmentSize <= 0 {
		return fmt.Errorf("invalid chunkSegmentSize: %d", chunkSegmentSize)
	}

	data := chunk.Data
	dataLen := len(data)
	if dataLen == 0 {
		return nil
	}

	chunkCid := chunk.ChunkCid
	chunkSegmentSizeInt := int(chunkSegmentSize)

	for i := 0; i < dataLen; i += chunkSegmentSizeInt {
		end := i + chunkSegmentSizeInt
		if end > dataLen {
			end = dataLen
		}

		fileChunk := &pb.FileChunkData{
			Data:     data[i:end:end],
			ChunkCid: chunkCid,
		}

		if err := sender(fileChunk); err != nil {
			return err
		}
	}

	return nil
}

// CreateFileDownload creates a new download request.
func (sdk *SDK) CreateFileDownload(ctx context.Context, bucketName string, rootCID string) (_ FileDownload, err error) {
	defer mon.Task()(&ctx, bucketName, rootCID)(&err)
	if bucketName == "" {
		return FileDownload{}, errSDK.Errorf("empty bucket name")
	}
	if rootCID == "" {
		return FileDownload{}, errSDK.Errorf("empty root cid")
	}

	res, err := sdk.client.FileDownloadCreate(ctx, &pb.FileDownloadCreateRequest{
		BucketName: bucketName,
		RootCid:    rootCID,
	})
	if err != nil {
		return FileDownload{}, errSDK.Wrap(err)
	}

	chunks := make([]FileChunk, len(res.Chunks))
	for i, chunk := range res.Chunks {
		chunks[i] = FileChunk{
			CID:         chunk.Cid,
			Size:        chunk.Size,
			NodeID:      chunk.NodeId,
			NodeAddress: chunk.NodeAddress,
			Permit:      chunk.Permit,
		}
	}

	return FileDownload{Chunks: chunks}, nil
}

// CreateFileDownloadV2 creates a new download request.
func (sdk *SDK) CreateFileDownloadV2(ctx context.Context, bucketID string, fileName string) (_ FileDownload, innerErr error) {
	defer mon.Task()(&ctx, bucketID, fileName)(&innerErr)
	if bucketID == "" {
		return FileDownload{}, errSDK.Errorf("empty bucket id")
	}

	if fileName == "" {
		return FileDownload{}, errSDK.Errorf("empty file name")
	}

	res, err := sdk.client.FileDownloadCreateV2(ctx, &pb.FileDownloadCreateV2Request{
		BucketName: bucketID,
		FileName:   fileName,
	})
	if err != nil {
		return FileDownload{}, errSDK.Wrap(err)
	}

	chunks := make([]FileChunk, len(res.Chunks))
	for i, chunk := range res.Chunks {
		chunks[i] = FileChunk{
			CID:         chunk.Cid,
			Size:        chunk.Size,
			NodeID:      chunk.NodeId,
			NodeAddress: chunk.NodeAddress,
			Permit:      chunk.Permit,
		}
	}

	return FileDownload{Chunks: chunks}, nil
}

// Download function downloads a file from FileDownload.
func (sdk *SDK) Download(ctx context.Context, fileDownload FileDownload, writer io.Writer) (err error) {
	defer mon.Task()(&ctx, fileDownload)(&err)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(sdk.maxConcurrency)

	pool := newConnectionPool()
	defer func() {
		if err := pool.close(); err != nil {
			slog.Warn("failed to close connection", err)
		}
	}()
	type retrievedChunk struct {
		Pos  int
		CID  string
		Data []byte
	}
	ch := make(chan retrievedChunk, len(fileDownload.Chunks))

	for i, chunk := range fileDownload.Chunks {
		i, chunk := i, chunk
		deriveCtx := context.WithoutCancel(ctx)
		g.Go(func() (err error) {
			defer mon.Task()(&deriveCtx, chunk.CID)(&err)

			client, closer, err := pool.createClient(chunk.NodeAddress, sdk.useConnectionPool)
			if err != nil {
				return err
			}
			if closer != nil {
				defer func() {
					if closeErr := closer(); closeErr != nil {
						slog.Warn("failed to close connection", slog.Int("num", i), chunk.CID, chunk.NodeAddress, closeErr)
					}
				}()
			}

			downloadClient, err := client.FileDownloadChunk(ctx, &pb.FileDownloadChunkRequest{
				ChunkCid: chunk.CID,
			})
			if err != nil {
				return err
			}

			defer func() {
				if closeErr := downloadClient.CloseSend(); closeErr != nil {
					slog.Warn("failed to close and send", slog.Int("num", i), chunk.CID, chunk.NodeAddress, closeErr)
				}
			}()

			var buf bytes.Buffer
			for {
				chunkData, err := downloadClient.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}

				_, _ = buf.Write(chunkData.Data)
			}

			ch <- retrievedChunk{
				Pos:  i,
				CID:  chunk.CID,
				Data: buf.Bytes(),
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errSDK.Wrap(err)
	}

	close(ch)

	chunks := make([]retrievedChunk, 0)
	for retrieved := range ch {
		chunks = append(chunks, retrieved)
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		return chunks[j].Pos > chunks[i].Pos
	})
	for _, chunk := range chunks {
		id, err := cid.Decode(chunk.CID)
		if err != nil {
			return err
		}

		var data []byte
		switch id.Type() {
		case cid.DagProtobuf:
			node, err := merkledag.DecodeProtobuf(chunk.Data)
			if err != nil {
				return errSDK.Wrap(err)
			}
			fsNode, err := unixfs.FSNodeFromBytes(node.Data())
			if err != nil {
				return errSDK.Wrap(err)
			}
			data = fsNode.Data()
		case cid.Raw:
			data = chunk.Data
		default:
			return errSDK.Errorf("unknown cid type: %v", id.Type())
		}

		if _, err := writer.Write(data); err != nil {
			return err
		}
	}

	return nil
}

type connectionPool struct {
	mu                sync.RWMutex
	connections       map[string]*grpc.ClientConn
	useConnectionPool bool
}

func newConnectionPool() *connectionPool {
	return &connectionPool{
		connections: make(map[string]*grpc.ClientConn),
	}
}
func (p *connectionPool) createClient(addr string, pooled bool) (pb.NodeAPIClient, func() error, error) {
	if pooled {
		conn, err := p.get(addr)
		if err != nil {
			return nil, nil, err
		}
		return pb.NewNodeAPIClient(conn), nil, nil
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewNodeAPIClient(conn), conn.Close, nil
}

func (p *connectionPool) get(addr string) (*grpc.ClientConn, error) {
	p.mu.RLock()
	if conn, exists := p.connections[addr]; exists {
		p.mu.RUnlock()
		return conn, nil
	}
	p.mu.RUnlock()

	// Lock to prevent race condition
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check to see if another goroutine has added the connection
	if conn, exists := p.connections[addr]; exists {
		return conn, nil
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	// Add the new connection to the pool
	p.connections[addr] = conn

	return conn, nil
}

func (p *connectionPool) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errList []error

	for addr, conn := range p.connections {
		if err := conn.Close(); err != nil {
			errList = append(errList, fmt.Errorf("failed to close connection to %s: %w", addr, err))
		}
		delete(p.connections, addr)
	}

	if len(errList) > 0 {
		return fmt.Errorf("encountered errors while closing connections: %v", errList)
	}

	return nil
}
