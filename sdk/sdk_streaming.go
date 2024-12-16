// Copyright (C) 2024 Akave
// See LICENSE for copying information.

package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"golang.org/x/exp/maps"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"akave.ai/akavesdk/private/cryptoutils"
	"akave.ai/akavesdk/private/encryption"
	"akave.ai/akavesdk/private/pb"
)

// StreamingAPI exposes SDK file streaming API.
type StreamingAPI struct {
	client            pb.StreamAPIClient
	conn              *grpc.ClientConn
	erasureCode       *ErasureCode
	maxConcurrency    int
	blockPartSize     int64
	useConnectionPool bool
	encryptionKey     []byte // empty means no encryption
	maxBlocksInChunk  int
}

// FileInfo returns meta information for single file by bucket and file name.
func (sdk *StreamingAPI) FileInfo(ctx context.Context, bucketName string, fileName string) (_ FileMetaV2, err error) {
	defer mon.Task()(&ctx, bucketName, fileName)(&err)
	if bucketName == "" {
		return FileMetaV2{}, errSDK.Errorf("empty bucket name")
	}
	if fileName == "" {
		return FileMetaV2{}, errSDK.Errorf("empty file name")
	}

	res, err := sdk.client.FileView(ctx, &pb.StreamFileViewRequest{
		BucketName: bucketName,
		FileName:   fileName,
	})
	if err != nil {
		return FileMetaV2{}, errSDK.Wrap(err)
	}

	return FileMetaV2{
		StreamID:    res.GetStreamId(),
		RootCID:     res.GetRootCid(),
		BucketID:    res.GetBucketId(),
		Name:        res.GetFileName(),
		EncodedSize: res.GetEncodedSize(),
		Size:        res.GetSize(),
		CreatedAt:   res.CreatedAt.AsTime(),
		CommitedAt:  res.CommittedAt.AsTime(),
	}, nil
}

// ListFiles returns list of files in a particular bucket.
func (sdk *StreamingAPI) ListFiles(ctx context.Context, bucketName string) (_ []FileMetaV2, err error) {
	defer mon.Task()(&ctx, bucketName)(&err)

	if bucketName == "" {
		return nil, errSDK.Errorf("empty bucket name")
	}

	resp, err := sdk.client.FileList(ctx, &pb.StreamFileListRequest{
		BucketName: bucketName,
	})
	if err != nil {
		return nil, errSDK.Wrap(err)
	}

	files := make([]FileMetaV2, 0, len(resp.Files))
	for _, fileMeta := range resp.Files {
		files = append(files, toFileMeta(fileMeta, bucketName))
	}

	return files, nil
}

// FileVersions returns list of files in a particular bucket.
func (sdk *StreamingAPI) FileVersions(ctx context.Context, bucketName, fileName string) (_ []FileMetaV2, err error) {
	defer mon.Task()(&ctx, bucketName, fileName)(&err)

	if bucketName == "" {
		return nil, errSDK.Errorf("empty bucket name")
	}

	resp, err := sdk.client.FileVersions(ctx, &pb.StreamFileListVersionsRequest{
		BucketName: bucketName,
		FileName:   fileName,
	})
	if err != nil {
		return nil, errSDK.Wrap(err)
	}

	files := make([]FileMetaV2, 0, len(resp.Versions))
	for _, fileMeta := range resp.Versions {
		files = append(files, toFileMeta(fileMeta, bucketName))
	}

	return files, nil
}

// CreateFileUpload creates a new file upload request.
func (sdk *StreamingAPI) CreateFileUpload(ctx context.Context, bucketName string, fileName string) (_ FileUploadV2, err error) {
	defer mon.Task()(&ctx, bucketName, fileName)(&err)

	if bucketName == "" {
		return FileUploadV2{}, errSDK.Errorf("empty bucket name")
	}

	req := &pb.StreamFileUploadCreateRequest{
		BucketName: bucketName,
		FileName:   fileName,
	}

	res, err := sdk.client.FileUploadCreate(ctx, req)
	if err != nil {
		return FileUploadV2{}, errSDK.Wrap(err)
	}

	return FileUploadV2{
		BucketID:  res.BucketId,
		Name:      res.FileName,
		StreamID:  res.StreamId,
		CreatedAt: res.CreatedAt.AsTime(),
	}, nil
}

// Upload uploads a file using streaming api.
func (sdk *StreamingAPI) Upload(ctx context.Context, upload FileUploadV2, reader io.Reader) (_ FileMetaV2, err error) {
	defer mon.Task()(&ctx, upload)(&err)

	chunkEncOverhead := 0
	fileEncKey, err := encryptionKey(sdk.encryptionKey, upload.BucketID, upload.Name)
	if err != nil {
		return FileMetaV2{}, errSDK.Wrap(err)
	}
	if len(fileEncKey) > 0 {
		chunkEncOverhead = EncryptionOverhead
	}

	isEmptyFile := true

	bufferSize := sdk.maxBlocksInChunk * int(BlockSize)
	if sdk.erasureCode != nil { // erasure coding enabled
		bufferSize = sdk.erasureCode.DataBlocks * int(BlockSize)
	}
	bufferSize -= chunkEncOverhead
	buf := make([]byte, bufferSize)

	dagRoot, err := NewDAGRoot()
	if err != nil {
		return FileMetaV2{}, errSDK.Wrap(err)
	}

	var i int64
	for ; ; i++ {
		select {
		case <-ctx.Done():
			return FileMetaV2{}, ctx.Err()
		default:
		}

		n, err := io.ReadAtLeast(reader, buf, 1)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if isEmptyFile {
					return FileMetaV2{}, errSDK.Errorf("empty file")
				}
				break
			}
			return FileMetaV2{}, err
		}
		isEmptyFile = false

		chunkUpload, err := sdk.createChunkUpload(ctx, upload, i, fileEncKey, buf[:n])
		if err != nil {
			return FileMetaV2{}, err
		}

		if err := dagRoot.AddLink(chunkUpload.ChunkCID, chunkUpload.RawDataSize, chunkUpload.ProtoNodeSize); err != nil {
			return FileMetaV2{}, errSDK.Wrap(err)
		}

		if err := sdk.uploadChunk(ctx, chunkUpload); err != nil {
			return FileMetaV2{}, err
		}
	}

	rootCID, err := dagRoot.Build()
	if err != nil {
		return FileMetaV2{}, errSDK.Wrap(err)
	}

	fileMeta, err := sdk.commitStream(ctx, upload, rootCID.String(), i)
	if err != nil {
		return FileMetaV2{}, err
	}

	return fileMeta, nil
}

// CreateFileDownload creates a new download request.
// rootCID is optional and can be empty. Required when you want to dwonload a specific version of the file.
func (sdk *StreamingAPI) CreateFileDownload(ctx context.Context, bucketName, fileName, rootCID string) (_ FileDownloadV2, err error) {
	defer mon.Task()(&ctx, bucketName, fileName, rootCID)(&err)

	res, err := sdk.client.FileDownloadCreate(ctx, &pb.StreamFileDownloadCreateRequest{
		BucketName: bucketName,
		FileName:   fileName,
		RootCid:    rootCID,
	})
	if err != nil {
		return FileDownloadV2{}, errSDK.Wrap(err)
	}

	chunks := make([]Chunk, len(res.Chunks))
	for i, chunk := range res.Chunks {
		chunks[i] = Chunk{
			CID:         chunk.Cid,
			EncodedSize: chunk.EncodedSize,
			Size:        chunk.Size,
			Index:       int64(i),
		}
	}

	return FileDownloadV2{
		StreamID: res.StreamId,
		BucketID: res.BucketId,
		Name:     fileName,
		Chunks:   chunks,
	}, nil
}

// CreateRangeFileDownload creates a new download request with block ranges.
func (sdk *StreamingAPI) CreateRangeFileDownload(ctx context.Context, bucketName, fileName string, start, end int64) (_ FileDownloadV2, err error) {
	defer mon.Task()(&ctx, bucketName, fileName, start, end)(&err)

	res, err := sdk.client.FileDownloadRangeCreate(ctx, &pb.StreamFileDownloadRangeCreateRequest{
		BucketName: bucketName,
		FileName:   fileName,
		StartIndex: start,
		EndIndex:   end,
	})
	if err != nil {
		return FileDownloadV2{}, errSDK.Wrap(err)
	}

	chunks := make([]Chunk, len(res.Chunks))
	for i, chunk := range res.Chunks {
		chunks[i] = Chunk{
			CID:         chunk.Cid,
			EncodedSize: chunk.EncodedSize,
			Size:        chunk.Size,
			Index:       int64(i) + start,
		}
	}

	return FileDownloadV2{
		StreamID: res.StreamId,
		BucketID: res.BucketId,
		Name:     fileName,
		Chunks:   chunks,
	}, nil
}

// Download downloads a file using streaming api.
func (sdk *StreamingAPI) Download(ctx context.Context, fileDownload FileDownloadV2, writer io.Writer) (err error) {
	defer mon.Task()(&ctx, fileDownload)(&err)

	fileEncKey, err := encryptionKey(sdk.encryptionKey, fileDownload.BucketID, fileDownload.Name)
	if err != nil {
		return errSDK.Wrap(err)
	}

	for _, chunk := range fileDownload.Chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunkDownload, err := sdk.createChunkDownload(ctx, fileDownload.StreamID, chunk)
		if err != nil {
			return err
		}

		if err := sdk.downloadChunkBlocks(ctx, fileDownload.StreamID, chunkDownload, fileEncKey, writer); err != nil {
			return err
		}
	}

	return nil
}

// DownloadRandom downloads a file using streaming api and fetches only randomly half of the blocks.
func (sdk *StreamingAPI) DownloadRandom(ctx context.Context, fileDownload FileDownloadV2, writer io.Writer) (err error) {
	defer mon.Task()(&ctx, fileDownload)(&err)

	if sdk.erasureCode == nil {
		return errSDK.Errorf("erasure coding is not enabled")
	}

	fileEncKey, err := encryptionKey(sdk.encryptionKey, fileDownload.BucketID, fileDownload.Name)
	if err != nil {
		return errSDK.Wrap(err)
	}

	for _, chunk := range fileDownload.Chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunkDownload, err := sdk.createChunkDownload(ctx, fileDownload.StreamID, chunk)
		if err != nil {
			return err
		}

		if err := sdk.downloadRandomChunkBlocks(ctx, fileDownload.StreamID, chunkDownload, fileEncKey, writer); err != nil {
			return err
		}
	}

	return nil
}

// FileDelete deletes a file from a bucket.
func (sdk *StreamingAPI) FileDelete(ctx context.Context, bucketName, fileName string) (err error) {
	defer mon.Task()(&ctx, bucketName, fileName)(&err)

	_, err = sdk.client.FileDelete(ctx, &pb.StreamFileDeleteRequest{
		BucketName: bucketName,
		FileName:   fileName,
	})
	return errSDK.Wrap(err)
}

func (sdk *StreamingAPI) createChunkUpload(ctx context.Context, fileUpload FileUploadV2, index int64, fileEncryptionKey, data []byte) (_ FileChunkUploadV2, err error) {
	defer mon.Task()(&ctx, fileUpload, index)(&err)

	if len(fileEncryptionKey) > 0 {
		data, err = encryption.Encrypt(fileEncryptionKey, data, []byte(fmt.Sprintf("%d", index)))
		if err != nil {
			return FileChunkUploadV2{}, errSDK.Wrap(err)
		}
	}

	size := int64(len(data))
	blockSize := BlockSize.ToInt64()
	if sdk.erasureCode != nil { // erasure coding is enabled
		data, err = sdk.erasureCode.Encode(data)
		if err != nil {
			return FileChunkUploadV2{}, errSDK.Wrap(err)
		}
		// equivalent to notion of shard size in erasure coding terminology
		blockSize = int64(len(data) / (sdk.erasureCode.DataBlocks + sdk.erasureCode.ParityBlocks))
	}

	chunkDAG, err := BuildDAG(ctx, bytes.NewBuffer(data), blockSize, nil)
	if err != nil {
		return FileChunkUploadV2{}, err
	}

	protoChunk := toProtoChunk(fileUpload.StreamID, chunkDAG.CID.String(), index, size, chunkDAG.Blocks)
	req := &pb.StreamFileUploadChunkCreateRequest{Chunk: protoChunk}

	res, err := sdk.client.FileUploadChunkCreate(ctx, req)
	if err != nil {
		return FileChunkUploadV2{}, errSDK.Wrap(err)
	}

	if len(res.Blocks) != len(chunkDAG.Blocks) {
		return FileChunkUploadV2{}, errSDK.Errorf("received unexpected amount of blocks %d, expected %d", len(res.Blocks), len(chunkDAG.Blocks))
	}
	for i, upload := range res.Blocks {
		if chunkDAG.Blocks[i].CID != upload.Cid {
			return FileChunkUploadV2{}, errSDK.Errorf("block CID mismatch at position %d", i)
		}
		chunkDAG.Blocks[i].NodeAddress = upload.NodeAddress
		chunkDAG.Blocks[i].NodeID = upload.NodeId
		chunkDAG.Blocks[i].Permit = upload.Permit
	}

	return FileChunkUploadV2{
		StreamID:      fileUpload.StreamID,
		Index:         index,
		ChunkCID:      chunkDAG.CID,
		ActualSize:    size,
		RawDataSize:   chunkDAG.RawDataSize,
		ProtoNodeSize: chunkDAG.ProtoNodeSize,
		Blocks:        chunkDAG.Blocks,
	}, nil
}

func (sdk *StreamingAPI) uploadChunk(ctx context.Context, fileChunkUpload FileChunkUploadV2) (err error) {
	defer mon.Task()(&ctx, fileChunkUpload)(&err)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(sdk.maxConcurrency)

	pool := newConnectionPool()
	defer func() {
		if err := pool.close(); err != nil {
			slog.Warn("failed to close connection", slog.String("error", err.Error()))
		}
	}()

	protoChunk := toProtoChunk(
		fileChunkUpload.StreamID,
		fileChunkUpload.ChunkCID.String(),
		fileChunkUpload.Index,
		fileChunkUpload.ActualSize,
		fileChunkUpload.Blocks,
	)
	for i, block := range fileChunkUpload.Blocks {
		deriveCtx := context.WithoutCancel(ctx)
		g.Go(func() (err error) {
			defer mon.Task()(&deriveCtx, block.CID)(&err)

			client, closer, err := pool.createStreamingClient(block.NodeAddress, sdk.useConnectionPool)
			if err != nil {
				return err
			}
			if closer != nil {
				defer func() {
					if closeErr := closer(); closeErr != nil {
						slog.Warn("failed to close connection",
							slog.Int("block_index", i),
							slog.String("block_cid", block.CID),
							slog.String("chunk_cid", protoChunk.Cid),
							slog.Int64("chunk_index", fileChunkUpload.Index),
							slog.String("node_address", block.NodeAddress),
							slog.String("error", closeErr.Error()),
						)
					}
				}()
			}

			sender, err := client.FileUploadBlock(ctx)
			if err != nil {
				return err
			}

			err = sdk.uploadBlock(ctx, &pb.StreamFileBlockData{
				Data:  block.Data,
				Cid:   block.CID,
				Index: int64(i),
				Chunk: protoChunk,
			}, sender.Send)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}

			_, closeErr := sender.CloseAndRecv()
			return closeErr
		})
	}

	if err := g.Wait(); err != nil {
		return errSDK.Wrap(err)
	}

	return nil
}

func (sdk *StreamingAPI) createChunkDownload(ctx context.Context, streamID string, chunk Chunk) (_ FileChunkDownloadV2, err error) {
	defer mon.Task()(&ctx, streamID, chunk)(&err)

	res, err := sdk.client.FileDownloadChunkCreate(ctx, &pb.StreamFileDownloadChunkCreateRequest{
		StreamId: streamID,
		ChunkCid: chunk.CID,
	})
	if err != nil {
		return FileChunkDownloadV2{}, errSDK.Wrap(err)
	}

	blocks := make([]FileBlock, len(res.Blocks))
	for i, block := range res.Blocks {
		blocks[i] = FileBlock{
			CID:         block.Cid,
			NodeID:      block.NodeId,
			NodeAddress: block.NodeAddress,
			Permit:      block.Permit,
		}
	}

	return FileChunkDownloadV2{
		CID:         chunk.CID,
		Index:       chunk.Index,
		EncodedSize: chunk.EncodedSize,
		Size:        chunk.Size,
		Blocks:      blocks,
	}, nil
}

func (sdk *StreamingAPI) downloadChunkBlocks(
	ctx context.Context,
	streamID string,
	chunkDownload FileChunkDownloadV2,
	fileEncryptionKey []byte,
	writer io.Writer,
) (err error) {

	defer mon.Task()(&ctx, streamID, chunkDownload)(&err)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(sdk.maxConcurrency)

	pool := newConnectionPool()
	defer func() {
		if err := pool.close(); err != nil {
			slog.Warn("failed to close connection", slog.String("error", err.Error()))
		}
	}()

	type retrievedBlock struct {
		Pos  int
		CID  string
		Data []byte
	}
	ch := make(chan retrievedBlock, len(chunkDownload.Blocks))

	for i, block := range chunkDownload.Blocks {
		deriveCtx := context.WithoutCancel(ctx)

		g.Go(func() (err error) {
			defer mon.Task()(&deriveCtx, block.CID)(&err)

			client, closer, err := pool.createStreamingClient(block.NodeAddress, sdk.useConnectionPool)
			if err != nil {
				return err
			}
			if closer != nil {
				defer func() {
					if closeErr := closer(); closeErr != nil {
						slog.Warn("failed to close connection",
							slog.Int("block_index", i),
							slog.String("block_cid", block.CID),
							slog.String("error", closeErr.Error()),
						)
					}
				}()
			}

			downloadClient, err := client.FileDownloadBlock(ctx, &pb.StreamFileDownloadBlockRequest{
				StreamId:   streamID,
				ChunkCid:   chunkDownload.CID,
				ChunkIndex: chunkDownload.Index,
				BlockCid:   block.CID,
				BlockIndex: int64(i),
			})
			if err != nil {
				return err
			}

			var buf bytes.Buffer
			for {
				blockData, err := downloadClient.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}
				_, _ = buf.Write(blockData.Data)
			}

			ch <- retrievedBlock{
				Pos:  i,
				CID:  block.CID,
				Data: buf.Bytes(),
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errSDK.Wrap(err)
	}

	close(ch)

	blocks := make([][]byte, len(chunkDownload.Blocks))
	for retrieved := range ch {
		data, err := ExtractBlockData(retrieved.CID, retrieved.Data)
		if err != nil {
			return errSDK.Wrap(err)
		}
		blocks[retrieved.Pos] = data
	}

	var data []byte
	if sdk.erasureCode != nil { // erasure coding is enabled
		data, err = sdk.erasureCode.ExtractData(blocks, int(chunkDownload.Size))
		if err != nil {
			return errSDK.Wrap(err)
		}
	} else {
		data = bytes.Join(blocks, nil)
	}

	if len(fileEncryptionKey) > 0 {
		data, err = encryption.Decrypt(fileEncryptionKey, data, []byte(fmt.Sprintf("%d", chunkDownload.Index)))
		if err != nil {
			return errSDK.Wrap(err)
		}
	}

	if _, err := writer.Write(data); err != nil {
		return errSDK.Wrap(err)
	}

	return nil
}

// !!!!use only with erasure coding!!!!
func (sdk *StreamingAPI) downloadRandomChunkBlocks(
	ctx context.Context,
	streamID string,
	chunkDownload FileChunkDownloadV2,
	fileEncryptionKey []byte,
	writer io.Writer,
) (err error) {

	defer mon.Task()(&ctx, streamID, chunkDownload)(&err)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(sdk.maxConcurrency)

	pool := newConnectionPool()
	defer func() {
		if err := pool.close(); err != nil {
			slog.Warn("failed to close connection", slog.String("error", err.Error()))
		}
	}()

	type retrievedBlock struct {
		Pos  int
		CID  string
		Data []byte
	}
	ch := make(chan retrievedBlock, len(chunkDownload.Blocks))

	blocksMap := make(map[int]FileBlock, len(chunkDownload.Blocks))
	for i, block := range chunkDownload.Blocks {
		blocksMap[i] = block
	}

	blockIndexes := maps.Keys(blocksMap)
	cryptoutils.Shuffle(blockIndexes)

	// take only half of the blocks
	for _, i := range blockIndexes[:sdk.erasureCode.DataBlocks] {
		delete(blocksMap, i)
	}

	for index, block := range blocksMap {
		deriveCtx := context.WithoutCancel(ctx)
		g.Go(func() (err error) {
			defer mon.Task()(&deriveCtx, block.CID)(&err)

			client, closer, err := pool.createStreamingClient(block.NodeAddress, sdk.useConnectionPool)
			if err != nil {
				return err
			}
			if closer != nil {
				defer func() {
					if closeErr := closer(); closeErr != nil {
						slog.Warn("failed to close connection",
							slog.Int("block_index", index),
							slog.String("block_cid", block.CID),
							slog.String("error", closeErr.Error()),
						)
					}
				}()
			}

			downloadClient, err := client.FileDownloadBlock(ctx, &pb.StreamFileDownloadBlockRequest{
				StreamId:   streamID,
				ChunkCid:   chunkDownload.CID,
				ChunkIndex: chunkDownload.Index,
				BlockCid:   block.CID,
				BlockIndex: int64(index),
			})
			if err != nil {
				return err
			}

			var buf bytes.Buffer
			for {
				blockData, err := downloadClient.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}
				_, _ = buf.Write(blockData.Data)
			}

			ch <- retrievedBlock{
				Pos:  index,
				CID:  block.CID,
				Data: buf.Bytes(),
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errSDK.Wrap(err)
	}

	close(ch)

	blocks := make([][]byte, len(chunkDownload.Blocks))
	for retrieved := range ch {
		data, err := ExtractBlockData(retrieved.CID, retrieved.Data)
		if err != nil {
			return errSDK.Wrap(err)
		}
		blocks[retrieved.Pos] = data
	}

	var data []byte
	if sdk.erasureCode != nil { // erasure coding is enabled
		data, err = sdk.erasureCode.ExtractData(blocks, int(chunkDownload.Size))
		if err != nil {
			return errSDK.Wrap(err)
		}
	} else {
		data = bytes.Join(blocks, nil)
	}

	if len(fileEncryptionKey) > 0 {
		data, err = encryption.Decrypt(fileEncryptionKey, data, []byte(fmt.Sprintf("%d", chunkDownload.Index)))
		if err != nil {
			return errSDK.Wrap(err)
		}
	}

	if _, err := writer.Write(data); err != nil {
		return errSDK.Wrap(err)
	}

	return nil
}

func (sdk *StreamingAPI) commitStream(ctx context.Context, upload FileUploadV2, rootCID string, chunkCount int64) (_ FileMetaV2, err error) {
	defer mon.Task()(&ctx, upload, rootCID, chunkCount)(&err)

	res, err := sdk.client.FileUploadCommit(ctx, &pb.StreamFileUploadCommitRequest{
		StreamId:   upload.StreamID,
		RootCid:    rootCID,
		ChunkCount: chunkCount,
	})
	if err != nil {
		return FileMetaV2{}, errSDK.Wrap(err)
	}

	return FileMetaV2{
		StreamID:    res.StreamId,
		RootCID:     rootCID,
		BucketID:    res.BucketId,
		Name:        res.FileName,
		EncodedSize: res.EncodedSize,
		Size:        res.Size,
		CreatedAt:   upload.CreatedAt,
		CommitedAt:  res.CommittedAt.AsTime(),
	}, nil
}

func (sdk *StreamingAPI) uploadBlock(ctx context.Context, block *pb.StreamFileBlockData, sender func(*pb.StreamFileBlockData) error) (err error) {
	defer mon.Task()(&ctx, block.Cid, block.Index, block.Chunk.Cid, block.Chunk.Index, block.Chunk.StreamId)(&err)

	data := block.Data
	dataLen := int64(len(data))
	if dataLen == 0 {
		return nil
	}

	i := int64(0)
	for ; i < dataLen; i += sdk.blockPartSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			end := i + sdk.blockPartSize
			if end > dataLen {
				end = dataLen
			}

			block.Data = data[i:end:end]

			if err := sender(block); err != nil {
				return err
			}

			// these fields are only required for the first part, skip them for the rest.
			block.Chunk = nil
			block.Cid = ""
		}
	}

	return nil
}

func toProtoChunk(streamID, cid string, index, size int64, blocks []FileBlock) *pb.Chunk {
	pbBlocks := make([]*pb.Chunk_Block, len(blocks))
	for i, block := range blocks {
		pbBlocks[i] = &pb.Chunk_Block{
			Cid:  block.CID,
			Size: int64(len(block.Data)),
		}
	}
	return &pb.Chunk{
		StreamId: streamID,
		Cid:      cid,
		Index:    index,
		Size:     size,
		Blocks:   pbBlocks,
	}
}

func toFileMeta(protoFile *pb.File, bucketName string) FileMetaV2 {
	return FileMetaV2{
		StreamID:    protoFile.StreamId,
		RootCID:     protoFile.RootCid,
		BucketID:    IDFromName(bucketName),
		Name:        protoFile.Name,
		EncodedSize: protoFile.EncodedSize,
		Size:        protoFile.Size,
		CreatedAt:   protoFile.CreatedAt.AsTime(),
		CommitedAt:  protoFile.CommitedAt.AsTime(),
	}
}

func IDFromName(bucketName string) string {
	h := sha256.New()
	h.Write([]byte(bucketName))
	return string(h.Sum(nil))
}
