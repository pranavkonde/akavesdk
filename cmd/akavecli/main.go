// Copyright (C) 2024 Akave
// See LICENSE for copying information.

// Package main provides a command-line interface for managing Akave SDK resources
// such as buckets and files.
//
// Usage:
//
//	akavecli [command] [flags]
//
// The available commands are:
//
//	bucket create <bucket-name>      Creates a new bucket
//	bucket view <bucket-name>     Views details of a specific bucket
//	bucket list                             Lists all buckets
//	file upload <bucket-name> <file-path>     Uploads a file to a bucket
//	file download <bucket-name> <file-name> <destination-path>   Downloads a file from a bucket
//
// Example:
//
//	export NODE_RPC_ADDRESS="localhost:5001"
//	akavecli bucket create "my-bucket"
//	akavecli bucket view "my-bucket"
//	akavecli bucket list
//
// Environment Variables:
//
//	NODE_RPC_ADDRESS  The RPC address of the Akave node to connect to.
//
// Flags:
//
//	--help    Show help for any command
//
// See each command's help for more details on usage.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/spacemonkeygo/monkit/v3/environment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	mJaeger "storj.io/monkit-jaeger"

	"akave.ai/akavesdk/private/memory"
	"akave.ai/akavesdk/sdk"
)

var (
	rootCmd = &cobra.Command{
		Use:   "akavecli",
		Short: "A CLI for managing Akave resources",
	}

	bucketCmd = &cobra.Command{
		Use:   "bucket",
		Short: "Manage buckets",
	}

	fileCmd = &cobra.Command{
		Use:   "file",
		Short: "Manage files in buckets",
	}

	bucketCreateCmd = &cobra.Command{
		Use:   "create",
		Short: "Creates a new bucket",
		Args:  cobra.ExactArgs(1),
		RunE:  cmdCreateBucket,
	}

	bucketViewCmd = &cobra.Command{
		Use:   "view",
		Short: "Views a bucket's details",
		Args:  cobra.ExactArgs(1),
		RunE:  cmdViewBucket,
	}

	bucketListCmd = &cobra.Command{
		Use:   "list",
		Short: "Lists all buckets",
		Args:  cobra.NoArgs,
		RunE:  cmdListBuckets,
	}

	fileListCmd = &cobra.Command{
		Use:   "list",
		Short: "Lists all files in a bucket",
		Args:  cobra.ExactArgs(1),
		RunE:  cmdListFiles,
	}

	fileInfoCmd = &cobra.Command{
		Use:   "info",
		Short: "Retrieves file information",
		Args:  cobra.ExactArgs(2),
		RunE:  cmdFileInfo,
	}

	fileUploadCmd = &cobra.Command{
		Use:   "upload",
		Short: "Uploads a file to a bucket",
		Args:  cobra.ExactArgs(2),
		RunE:  cmdFileUpload,
	}

	fileDownloadCmd = &cobra.Command{
		Use:   "download",
		Short: "Downloads a file from a bucket",
		Args:  cobra.ExactArgs(3),
		RunE:  cmdFileDownload,
	}

	nodeRPCAddress    string
	maxConcurrency    int
	chunkSegmentSize  int64
	useConnectionPool bool

	// tracing.
	mon = monkit.Package()

	tracingAgentAddr = "localhost:6831"
	collectorAddr    = "http://localhost:14268/api/traces"
	serviceName      = "akavecli"
)

func init() {
	bucketCmd.AddCommand(bucketCreateCmd)
	bucketCmd.AddCommand(bucketViewCmd)
	bucketCmd.AddCommand(bucketListCmd)
	fileCmd.AddCommand(fileListCmd)
	fileCmd.AddCommand(fileInfoCmd)
	fileCmd.AddCommand(fileUploadCmd)
	fileCmd.AddCommand(fileDownloadCmd)
	rootCmd.AddCommand(bucketCmd)
	rootCmd.AddCommand(fileCmd)
}

func initFlags() {
	rootCmd.PersistentFlags().StringVar(&nodeRPCAddress, "node-address", "", "The address of the node RPC")
	rootCmd.PersistentFlags().IntVar(&maxConcurrency, "maxConcurrency", 10, "Maximum concurrency level")
	rootCmd.PersistentFlags().Int64Var(&chunkSegmentSize, "chunkSegmentSize", int64(memory.MB)*1, "Size of each chunk segment")
	rootCmd.PersistentFlags().BoolVar(&useConnectionPool, "useConnectionPool", true, "Use connection pool")

	if err := rootCmd.MarkPersistentFlagRequired("node-address"); err != nil {
		log.Fatal(err)
	}
}

func initTracing() (*mJaeger.ThriftCollector, func()) {
	log := zap.NewExample()
	collector, err := mJaeger.NewThriftCollector(log, tracingAgentAddr, serviceName, []mJaeger.Tag{}, 200, 0, 1*time.Nanosecond)
	if err != nil {
		log.Error("failed to create collector", zap.Error(err))
	}
	unreg := mJaeger.RegisterJaeger(monkit.Default, collector, mJaeger.Options{
		Fraction: 1,
	})

	return collector, unreg
}

func main() {
	environment.Register(monkit.Default)
	collector, unreg := initTracing()
	defer unreg()

	var eg errgroup.Group
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eg.Go(func() error {
		collector.Run(ctx)
		return nil
	})

	log.SetOutput(os.Stderr)

	initFlags()

	if err := rootCmd.Execute(); err != nil {
		rootCmd.PrintErrf("failed to execute root cmd: %v", err)
	}
	cancel()

	err := eg.Wait()
	if err != nil {
		rootCmd.PrintErrf("failed to wait for errgroup: %v", err)
	}
}

func cmdCreateBucket(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	bucketName := args[0]
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}

	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	result, err := sdk.CreateBucket(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}

	cmd.PrintErrf("Bucket created: ID=%s, CreatedAt=%s\n", result.ID, result.CreatedAt)

	return nil
}

func cmdViewBucket(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	bucketName := args[0]
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}

	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	result, err := sdk.ViewBucket(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to get bucket: %w", err)
	}

	cmd.PrintErrf("Bucket: ID=%s, Name=%s, CreatedAt=%s\n", result.ID, result.Name, result.CreatedAt)

	return nil
}

func cmdListBuckets(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	buckets, err := sdk.ListBuckets(ctx)
	if err != nil {
		return fmt.Errorf("failed to list buckets: %w", err)
	}

	for _, bucket := range buckets {
		cmd.PrintErrf("Bucket: ID=%s, Name=%s, CreatedAt=%s\n", bucket.ID, bucket.Name, bucket.CreatedAt)
	}

	return nil
}

func cmdListFiles(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	bucketName := args[0]
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}

	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	files, err := sdk.ListFiles(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	for _, file := range files {
		cmd.PrintErrf("File: Name=%s, RootCID=%s, Size=%d, CreatedAt=%s\n", file.Name, file.RootCID, file.Size, file.CreatedAt)
	}

	return nil
}

func cmdFileInfo(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	bucketName := args[0]
	fileName := args[1]
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	if fileName == "" {
		return fmt.Errorf("file name is required")
	}

	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	file, err := sdk.FileInfo(ctx, bucketName, fileName)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	cmd.PrintErrf("File: Name=%s, RootCID=%s, Size=%d, CreatedAt=%s\n", file.Name, file.RootCID, file.Size, file.CreatedAt)

	return nil
}

func cmdFileUpload(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	bucketName := args[0]
	filePath := args[1]

	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	if filePath == "" {
		return fmt.Errorf("file path is required")
	}

	fileName := filepath.Base(filePath)

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			cmd.PrintErrf("failed to close file: %v", cerr)
		}
	}()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	fileUpload, err := sdk.CreateFileUpload(ctx, bucketName, fileName, fileInfo.Size(), file)
	if err != nil {
		return fmt.Errorf("failed to create file upload: %w", err)
	}

	err = sdk.Upload(ctx, fileUpload)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	cmd.PrintErrf("File uploaded successfully: Name=%s, RootCID=%s\n", fileName, fileUpload.RootCID)

	return nil
}

func cmdFileDownload(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	defer mon.Task()(&ctx, args)(&err)
	bucketName := args[0]
	fileName := args[1]
	destPath := args[2]
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	if fileName == "" {
		return fmt.Errorf("file name is required")
	}
	if destPath == "" {
		return fmt.Errorf("destination path is required")
	}

	sdk, err := sdk.New(nodeRPCAddress, maxConcurrency, chunkSegmentSize, useConnectionPool)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sdk.Close(); cerr != nil {
			cmd.PrintErrf("failed to close SDK: %v", cerr)
		}
	}()

	fileDownload, err := sdk.CreateFileDownloadV2(ctx, bucketName, fileName)
	if err != nil {
		return fmt.Errorf("failed to create file download: %w", err)
	}

	outFile, err := os.Create(filepath.Join(destPath, fileName))
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		if cerr := outFile.Close(); cerr != nil && err == nil {
			cmd.PrintErrf("failed to close destination file: %v", cerr)
		}
	}()

	err = sdk.Download(ctx, fileDownload, outFile)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	cmd.PrintErrf("File downloaded successfully: Name=%s, Path=%s\n", fileName, filepath.Join(destPath, fileName))
	return nil
}
