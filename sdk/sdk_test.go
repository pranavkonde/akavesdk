// Copyright (C) 2024 Akave
// See LICENSE for copying information.

package sdk_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"io"
	rand2 "math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"akave.ai/akavesdk/private/memory"
	"akave.ai/akavesdk/sdk"
)

const maxConcurrency = 5
const chunkSegmentSize = 1 * memory.MB

func TestCreateBucket(t *testing.T) {
	akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, akave.Close())
	})

	bucketName := randomBucketName(t, 12)
	bucket, err := akave.CreateBucket(context.Background(), bucketName)
	require.NoError(t, err)
	require.NotEmpty(t, bucket.CreatedAt)
	require.NotEmpty(t, bucket.ID)
}

func TestViewBucket(t *testing.T) {
	akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, akave.Close())
	})

	bucketName := randomBucketName(t, 10)
	// create bucket
	bucket, err := akave.CreateBucket(context.Background(), bucketName)
	require.NoError(t, err)
	expected := sdk.Bucket{
		ID:        bucket.ID,
		Name:      bucketName,
		CreatedAt: bucket.CreatedAt,
	}
	actual, err := akave.ViewBucket(context.Background(), bucketName)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestListBuckets(t *testing.T) {
	akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, akave.Close())
	})

	expected := make([]sdk.Bucket, 0)
	for i := 0; i < 3; i++ {
		bucketName := randomBucketName(t, 10)
		// create bucket
		bucket, err := akave.CreateBucket(context.Background(), bucketName)
		require.NoError(t, err)
		expected = append(expected, sdk.Bucket{
			ID:        bucket.ID,
			Name:      bucketName,
			CreatedAt: bucket.CreatedAt,
		})
	}

	buckets, err := akave.ListBuckets(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(buckets), 3)
	require.True(t, assert.Subset(t, buckets, expected))
}

func TestUploadDownload(t *testing.T) {
	tests := []struct {
		name     string
		fileSize int // Size in MB
		generate func(testing.TB, int64, int) *bytes.Buffer
	}{
		{"10MBUploadDownload", 10, generateFile},
		{"100MBUploadDownload", 100, generateFile},
		{"256MBUploadDownload", 256, generateFile},
		{"512MBUploadDownload", 512, generateFile},
		{"1GBUploadDownload", 1024, generateFile},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
			require.NoError(t, err)

			file := tc.generate(t, 2024, tc.fileSize*memory.MB.ToInt())

			bucketName := randomBucketName(t, 10)
			_, err = akave.CreateBucket(context.Background(), bucketName)
			require.NoError(t, err)

			fileUpload, err := akave.CreateFileUpload(context.Background(), bucketName, "example.txt", int64(len(file.Bytes())), file)
			require.NoError(t, err)
			require.NotEmpty(t, fileUpload.BucketName)
			require.NotEmpty(t, fileUpload.FileName)
			require.True(t, fileUpload.FileSize > 0)
			require.Greater(t, len(fileUpload.Chunks), 0)
			require.NotEmpty(t, fileUpload.RootCID)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			upResult := akave.Upload(ctx, fileUpload)
			require.NoError(t, upResult)

			var downloaded bytes.Buffer
			fileDownload, err := akave.CreateFileDownload(context.Background(), fileUpload.BucketName, fileUpload.RootCID)
			require.NoError(t, err)
			require.True(t, len(fileDownload.Chunks) > 0)

			ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel2()
			err = akave.Download(ctx2, fileDownload, &downloaded)
			require.NoError(t, err)

			file2 := tc.generate(t, 2024, tc.fileSize*memory.MB.ToInt())
			expected := file2.Bytes()
			checkFileContents(t, 10, expected, downloaded.Bytes())
		})
	}
}

func TestUploadDownloadV2(t *testing.T) {
	testUploadDownloadV2(t, 10*memory.MB.ToInt())
}
func Test1KBFileUploadDownloadV2(t *testing.T) {
	testUploadDownloadV2(t, 1*memory.KB.ToInt())
}
func Test127BFileUploadDownloadV2(t *testing.T) {
	testUploadDownloadV2(t, 127) // 127 bytes
}
func testUploadDownloadV2(t *testing.T, fileSize int) {
	akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, akave.Close())
	})

	file := generateFile(t, 2024, fileSize)

	bucketName := randomBucketName(t, 10)
	// create bucket
	_, err = akave.CreateBucket(context.Background(), bucketName)
	require.NoError(t, err)

	fileUpload, err := akave.CreateFileUpload(context.Background(), bucketName, "example.txt", int64(fileSize), file)
	require.NoError(t, err)
	require.NotEmpty(t, fileUpload.BucketName)
	require.NotEmpty(t, fileUpload.FileName)
	require.True(t, fileUpload.FileSize > 0)
	require.Greater(t, len(fileUpload.Chunks), 0)
	require.NotEmpty(t, fileUpload.RootCID)

	upResult := akave.Upload(context.Background(), fileUpload)
	require.NoError(t, upResult)

	var downloaded bytes.Buffer
	fileDownload, err := akave.CreateFileDownloadV2(context.Background(), fileUpload.BucketName, "example.txt")
	require.NoError(t, err)
	require.True(t, len(fileDownload.Chunks) > 0)

	// download file chunks
	err = akave.Download(context.Background(), fileDownload, &downloaded)
	require.NoError(t, err)

	f := generateFile(t, 2024, fileSize)
	expected := f.Bytes()
	checkFileContents(t, 10, expected, downloaded.Bytes())
}

func generateFile(t testing.TB, seed int64, size int) *bytes.Buffer {
	random := rand2.New(rand2.NewSource(seed))

	var buf bytes.Buffer
	buf.Grow(size)

	_, err := io.CopyN(&buf, random, int64(size))
	require.NoError(t, err)
	return &buf
}

func TestListFiles(t *testing.T) {
	ctx := context.Background()

	akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, akave.Close())
	})

	bucketName := randomBucketName(t, 10)
	_, err = akave.CreateBucket(ctx, bucketName)
	require.NoError(t, err)

	expected := make([]sdk.FileListItem, 0)
	for _, fileName := range []string{"foo", "bar", "baz"} {
		rootCID := uploadSimpleFile(ctx, t, akave, bucketName, fileName)

		expected = append(expected, sdk.FileListItem{
			RootCID: rootCID,
			Name:    fileName,
			Size:    2 * memory.MB.ToInt64(),
		})
	}

	list, err := akave.ListFiles(ctx, bucketName)
	require.NoError(t, err)
	// TODO: look for better solution
	for i, file := range list {
		require.WithinDuration(t, time.Now(), file.CreatedAt, 5*time.Second)
		list[i].CreatedAt = time.Time{}
	}
	require.ElementsMatch(t, expected, list)
}

func TestFileInfo(t *testing.T) {
	ctx := context.Background()
	bucketName := randomBucketName(t, 10)

	akave, err := sdk.New(PickNodeRPCAddress(t), maxConcurrency, chunkSegmentSize.ToInt64(), true)
	require.NoError(t, err)
	_, err = akave.CreateBucket(ctx, bucketName)
	require.NoError(t, err)

	rootCID := uploadSimpleFile(ctx, t, akave, bucketName, "example.txt")

	info, err := akave.FileInfo(ctx, bucketName, "example.txt")
	require.NoError(t, err)
	require.Equal(t, rootCID, info.RootCID)
	require.Equal(t, info.Name, "example.txt")
	require.Equal(t, info.Size, int64(2*memory.MB.ToInt()))
	require.WithinDuration(t, time.Now(), info.CreatedAt, 5*time.Second)
}

func uploadSimpleFile(ctx context.Context, t *testing.T, akave *sdk.SDK, bucket string, fileName string) string {
	f := generateAny2MBFile(t)

	upload, err := akave.CreateFileUpload(ctx, bucket, fileName, 2*memory.MB.ToInt64(), f)
	require.NoError(t, err)
	err = akave.Upload(ctx, upload)
	require.NoError(t, err)

	return upload.RootCID
}

func TestRandomFile(t *testing.T) {
	f1 := generate10MiBFile(t, 2025)
	f2 := generate10MiBFile(t, 2025)

	t.Log(f1.Bytes()[:20])
	t.Log(f2.Bytes()[:20])
	require.EqualValues(t, f1.Bytes(), f2.Bytes())
}

func generateAny2MBFile(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	buf.Grow(2 * memory.MB.ToInt())
	_, err := io.CopyN(&buf, rand.Reader, 2*memory.MB.ToInt64())
	require.NoError(t, err)
	return &buf
}

func generate10MiBFile(t *testing.T, seed int64) *bytes.Buffer {
	random := rand2.New(rand2.NewSource(seed))

	var buf bytes.Buffer
	buf.Grow(10 * memory.MiB.ToInt())

	_, err := io.CopyN(&buf, random, 10*memory.MiB.ToInt64())
	require.NoError(t, err)
	return &buf
}

func randomBucketName(t require.TestingT, size int) string {
	b := make([]byte, size)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// NodeRPCAddress is flag to set RPC address of akave node.
var NodeRPCAddress = flag.String("node-rpc-address", os.Getenv("NODE_RPC_ADDRESS"), "flag to set node rpc address")

// PickNodeRPCAddress picks node PRC address from flag.
func PickNodeRPCAddress(t testing.TB) string {
	if *NodeRPCAddress == "" || strings.EqualFold(*NodeRPCAddress, "omit") {
		t.Skip("node rpc address flag missing, example: -NODE_RPC_ADDRESS=<node rpc address>")
	}
	return *NodeRPCAddress
}

func TestCalculateDag(t *testing.T) {
	file := generate10MiBFile(t, 2024)
	actual, err := sdk.CalculateDAG(context.Background(), file, 1*memory.MiB.ToInt64())
	require.NotNil(t, actual)
	require.NoError(t, err)

	expected := expectedDAG(t)

	require.Equal(t, actual.RootCID.String(), expected.RootCID.String())
	require.Equal(t, len(actual.Chunks), len(expected.Chunks))

	for i := range actual.Chunks {
		require.Equal(t, actual.Chunks[i].CID, expected.Chunks[i].CID)
		require.Equal(t, actual.Chunks[i].Size, expected.Chunks[i].Size)
	}
}

func expectedDAG(t *testing.T) sdk.FileDAG {
	rootCid, err := cid.Parse("bafybeifir7qtrwocso27rscbwlf53p7na4ry3pyauoyilc22lotjkx4pji")
	require.NoError(t, err)

	return sdk.FileDAG{
		RootCID: rootCid,
		Chunks: []sdk.FileChunk{
			{
				CID:  "bafybeid3roxuooczpetsejm7xblw26rxohzjjl3xy3cnf6ovzfxxi3sapa",
				Size: 1048590,
			},
			{
				CID:  "bafybeigfjuysrwis5ynbcmrq2skbqx4htxx4i6dstqaqxgveje4wlw6b3m",
				Size: 1048590,
			},
			{
				CID:  "bafybeicth7txqbqzbv522rigdlznzf2d4t4fkbeaio4bznholhcjycydpa",
				Size: 1048590,
			},
			{
				CID:  "bafybeigf3eobgp665rmxndubsdft5pw7l6pgbgzmj4whhsplzyihdpkfzq",
				Size: 1048590,
			},
			{
				CID:  "bafybeidgkteds7m3h7vewpk5p2lbuqkijjyyzmi43tt4dpwaywwgzsaaui",
				Size: 1048590,
			},
			{
				CID:  "bafybeidje7v5yqm4vocwcsu44gvchdkfh6cc7ddycx43zrbxp5h7zzw5fe",
				Size: 1048590,
			},
			{
				CID:  "bafybeibb3v6eeo7diwpjjmvt7ikca4akbrusk3itzp2xshje3g35b26gie",
				Size: 1048590,
			},
			{
				CID:  "bafybeiflijdz4ia7yqsa736iws7nqwsvkrwot7x3aagc2mimgy4tbp4p3a",
				Size: 1048590,
			},
			{
				CID:  "bafybeib77hlwg5gn46ycgh4ml4iavt4a3byoc24grcmmy5gxznqwqnwkfa",
				Size: 1048590,
			},
			{
				CID:  "bafybeifqwspkmotwkeaxhus6mvvwys4rqem4p2h46bc3veeyqh5xbndgbm",
				Size: 1048590,
			},
		},
	}
}

func isPortBusy(port int) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// checks lengths, first and last N bytes.
func checkFileContents(t *testing.T, n int, expected, actual []byte) {
	require.Equal(t, len(expected), len(actual))
	// check first 10 bytes
	require.EqualValues(t, expected[:n], actual[:n])
	// check last 10 bytes
	require.EqualValues(t, expected[len(expected)-n:], actual[len(actual)-n:])
}
