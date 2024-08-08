// Copyright (C) 2024 Akave
// See LICENSE for copying information.

package main

import (
	"bytes"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

type testCase struct {
	name           string
	args           []string
	expectedOutput []string
	expectError    bool
}

func TestMain(m *testing.M) {
	initFlags()

	// Run tests
	code := m.Run()

	os.Exit(code)
}

func TestCreateBucketCommand(t *testing.T) {
	bucketName, err := randString(10)
	assert.NoError(t, err)
	nodeAddress := PickNodeRPCAddress(t)
	testCases := []testCase{
		{
			name:           "Create bucket successfully",
			args:           []string{"--node-address", nodeAddress, "bucket", "create", bucketName},
			expectedOutput: []string{"Bucket created"},
			expectError:    false,
		},
		{
			name:           "Create bucket already exists",
			args:           []string{"--node-address", nodeAddress, "bucket", "create", bucketName},
			expectedOutput: []string{"failed to create bucket: sdk: rpc error: code = AlreadyExists"},
			expectError:    true,
		},
		{
			name:           "Empty bucket name provided",
			args:           []string{"--node-address", nodeAddress, "bucket", "create", ""},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"--node-address", nodeAddress, "bucket", "create"},
			expectedOutput: []string{"accepts 1 arg(s), received 0"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func TestViewBucketCommand(t *testing.T) {
	nodeAddress := PickNodeRPCAddress(t)
	bucketName, err := randString(10)
	assert.NoError(t, err)
	_, stderr, err := captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName, "--node-address", nodeAddress})
	assert.NoError(t, err)
	bucketID, err := extractBucketID(stderr)
	assert.NoError(t, err)

	testCases := []testCase{
		{
			name:           "View bucket successfully",
			args:           []string{"bucket", "view", bucketName, "--node-address", nodeAddress},
			expectedOutput: []string{fmt.Sprintf("Bucket: ID=%s, Name=%s", bucketID, bucketName)},
			expectError:    false,
		},
		{
			name:           "View non-existent bucket",
			args:           []string{"bucket", "view", "error", "--node-address", nodeAddress},
			expectedOutput: []string{"failed to get bucket: sdk: rpc error: code = NotFound"},
			expectError:    true,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"bucket", "view", "", "--node-address", nodeAddress},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func TestListBucketsCommand(t *testing.T) {
	nodeAddress := PickNodeRPCAddress(t)
	bucketName1, err := randString(10)
	assert.NoError(t, err)
	_, stderr, err := captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName1, "--node-address", nodeAddress})
	assert.NoError(t, err)
	bucketID1, err := extractBucketID(stderr)
	assert.NoError(t, err)

	bucketName2, err := randString(10)
	assert.NoError(t, err)
	_, stderr, err = captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName2, "--node-address", nodeAddress})
	assert.NoError(t, err)
	bucketID2, err := extractBucketID(stderr)
	assert.NoError(t, err)

	testCases := []testCase{
		{
			name:           "List buckets successfully",
			args:           []string{"bucket", "list", "--node-address", nodeAddress},
			expectedOutput: []string{fmt.Sprintf("Bucket: ID=%s, Name=%s", bucketID1, bucketName1), fmt.Sprintf("Bucket: ID=%s, Name=%s", bucketID2, bucketName2)},
			expectError:    false,
		},
		{
			name:           "View non-existent bucket",
			args:           []string{"bucket", "view", "error", "--node-address", nodeAddress},
			expectedOutput: []string{"failed to get bucket: sdk: rpc error: code = NotFound"},
			expectError:    true,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"bucket", "view", "", "--node-address", nodeAddress},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func TestListFilesCommand(t *testing.T) {
	nodeAddress := PickNodeRPCAddress(t)
	bucketName, err := randString(10)
	assert.NoError(t, err)
	_, _, err = captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName, "--node-address", nodeAddress})
	assert.NoError(t, err)

	file1, err := createTempFile()
	assert.NoError(t, err)
	_, _, err = captureCobraOutput(rootCmd, []string{"file", "upload", bucketName, file1, "--node-address", nodeAddress})
	assert.NoError(t, err)

	file2, err := createTempFile()
	assert.NoError(t, err)
	_, _, err = captureCobraOutput(rootCmd, []string{"file", "upload", bucketName, file2, "--node-address", nodeAddress})
	assert.NoError(t, err)

	testCases := []testCase{
		{
			name:           "List files successfully",
			args:           []string{"file", "list", bucketName, "--node-address", nodeAddress},
			expectedOutput: []string{fmt.Sprintf("File: Name=%s", filepath.Base(file1)), fmt.Sprintf("File: Name=%s", filepath.Base(file2))},
			expectError:    false,
		},
		{
			name:           "List files for non-existent bucket",
			args:           []string{"file", "list", "nonexistent-bucket", "--node-address", nodeAddress},
			expectedOutput: []string{"failed to list files: sdk: rpc error: code = NotFound"},
			expectError:    true,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"file", "list", "", "--node-address", nodeAddress},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func TestFileInfoCommand(t *testing.T) {
	nodeAddress := PickNodeRPCAddress(t)
	bucketName, err := randString(10)
	assert.NoError(t, err)
	_, _, err = captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName, "--node-address", nodeAddress})
	assert.NoError(t, err)

	file, err := createTempFile()
	assert.NoError(t, err)
	_, stderr, err := captureCobraOutput(rootCmd, []string{"file", "upload", bucketName, file, "--node-address", nodeAddress})
	assert.NoError(t, err)
	rootCID, err := extractRootCID(stderr)
	assert.NoError(t, err)

	testCases := []testCase{
		{
			name:           "File info successfully",
			args:           []string{"file", "info", bucketName, filepath.Base(file), "--node-address", nodeAddress},
			expectedOutput: []string{fmt.Sprintf("File: Name=%s, RootCID=%s", filepath.Base(file), rootCID)},
			expectError:    false,
		},
		{
			name:           "File info for non-existent file",
			args:           []string{"file", "info", bucketName, "nonexistent-file", "--node-address", nodeAddress},
			expectedOutput: []string{"failed to get file info: sdk: rpc error: code = NotFound"},
			expectError:    true,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"file", "info", "", filepath.Base(file), "--node-address", nodeAddress},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
		{
			name:           "File name not provided",
			args:           []string{"file", "info", bucketName, "", "--node-address", nodeAddress},
			expectedOutput: []string{"file name is required"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func TestFileUploadCommand(t *testing.T) {
	nodeAddress := PickNodeRPCAddress(t)
	bucketName, err := randString(10)
	assert.NoError(t, err)
	_, _, err = captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName, "--node-address", nodeAddress})
	assert.NoError(t, err)

	file, err := createTempFile()
	assert.NoError(t, err)

	testCases := []testCase{
		{
			name:           "File upload successfully",
			args:           []string{"file", "upload", bucketName, file, "--node-address", nodeAddress},
			expectedOutput: []string{fmt.Sprintf("File uploaded successfully: Name=%s", filepath.Base(file))},
			expectError:    false,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"file", "upload", "", file, "--node-address", nodeAddress},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
		{
			name:           "File path not provided",
			args:           []string{"file", "upload", bucketName, "", "--node-address", nodeAddress},
			expectedOutput: []string{"file path is required"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func TestFileDownloadCommand(t *testing.T) {
	nodeAddress := PickNodeRPCAddress(t)
	bucketName, err := randString(10)
	assert.NoError(t, err)
	_, _, err = captureCobraOutput(rootCmd, []string{"bucket", "create", bucketName, "--node-address", nodeAddress})
	assert.NoError(t, err)

	file, err := createTempFile()
	assert.NoError(t, err)

	_, _, err = captureCobraOutput(rootCmd, []string{"file", "upload", bucketName, file, "--node-address", nodeAddress})
	assert.NoError(t, err)

	tempDir, err := os.MkdirTemp("", "test-download")
	assert.NoError(t, err)
	defer func() {
		err := os.RemoveAll(tempDir)
		assert.NoError(t, err)
	}()

	testCases := []testCase{
		{
			name:           "File download successfully",
			args:           []string{"file", "download", bucketName, filepath.Base(file), tempDir, "--node-address", nodeAddress},
			expectedOutput: []string{fmt.Sprintf("File downloaded successfully: Name=%s", filepath.Base(file))},
			expectError:    false,
		},
		{
			name:           "Bucket name not provided",
			args:           []string{"file", "download", "", filepath.Base(file), tempDir, "--node-address", nodeAddress},
			expectedOutput: []string{"bucket name is required"},
			expectError:    true,
		},
		{
			name:           "File name not provided",
			args:           []string{"file", "download", bucketName, "", tempDir, "--node-address", nodeAddress},
			expectedOutput: []string{"file name is required"},
			expectError:    true,
		},
		{
			name:           "Destination path not provided",
			args:           []string{"file", "download", bucketName, filepath.Base(file), "", "--node-address", nodeAddress},
			expectedOutput: []string{"destination path is required"},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureCobraOutput(rootCmd, tc.args)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for _, expected := range tc.expectedOutput {
				assert.Contains(t, stdout+stderr, expected)
			}
		})
	}
}

func randString(i int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, i)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return string(b), nil
}

func createTempFile() (string, error) {
	tempFile, err := os.CreateTemp("", "test-file")
	if err != nil {
		return "", err
	}
	txt, err := randString(1024)
	if err != nil {
		return "", err
	}
	_, err = tempFile.WriteString(txt)
	if err != nil {
		return "", err
	}
	err = tempFile.Close()
	if err != nil {
		return "", err
	}
	return tempFile.Name(), nil
}

func extractRootCID(output string) (string, error) {
	re := regexp.MustCompile(`RootCID=([a-zA-Z0-9]+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return "", fmt.Errorf("failed to extract root CID")
	}
	return matches[1], nil
}

func captureCobraOutput(cmd *cobra.Command, args []string) (string, string, error) {
	var stdoutBuf, stderrBuf bytes.Buffer

	cmd.SetOut(&stdoutBuf)
	cmd.SetErr(&stderrBuf)

	cmd.SetArgs(args)

	err := cmd.Execute()

	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	return stdout, stderr, err
}

func extractBucketID(logMessage string) (string, error) {
	pattern := `Bucket created: ID=([a-zA-Z0-9\-]+), CreatedAt=`

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to compile regex: %w", err)
	}

	matches := re.FindStringSubmatch(logMessage)
	if len(matches) < 2 {
		return "", fmt.Errorf("no ID found in log message")
	}

	id := matches[1]
	return id, nil
}

// NodeRPCAddress is flag to set RPC address of akave node.
var nodeAddress = flag.String("node-rpc-address", os.Getenv("NODE_RPC_ADDRESS"), "flag to set node rpc address")

// PickNodeRPCAddress picks node PRC address from flag.
func PickNodeRPCAddress(t testing.TB) string {
	if *nodeAddress == "" || strings.EqualFold(*nodeAddress, "omit") {
		t.Skip("node rpc address flag missing, example: -NODE_RPC_ADDRESS=<node rpc address>")
	}
	return *nodeAddress
}
