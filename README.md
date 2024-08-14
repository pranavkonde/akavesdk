# akavesdk

Base commit: e1a44a7181b072a42da6373f47f18dfd563f756f

## Build and test instructions
Requirments: Go 1.22+

`make build` - ouputs a cli binary into `bin/akavecli`.<br>
`make test` - runs tests.<br>
Look at `Makefile` for details.

## Usage
### Flags description
- `node-address` - address of a node you're connecting to.
- `maxConcurrency` - controls how many simulteneous requests to akave nodes can be in-flight(defaults to `10`)
- `useConnectionPool` - controls whether connections to akavenodes are pooled or new connection is created for each request(default to `true`)
- `chunkSegmentSize` - controls how large the chunk segment can be(a file is splitted to chunks and each chunks is streamed to akavenode; this flag controls the size of a single segment in this streaming process; defaults to `1 MB`).

### Commands
#### Bucket commands
- `bucket create <bucket-name>` - creates a new bucket
- `bucket view <bucket-name>` - view details of a specific bucket
- `bucket list` - list all buckets

#### File commands
- `file list <bucket-name>` - list all files in a bucket.
- `file info <bucket-name> <file-name>` - information about file.
- `file upload <bucket-name> <file-path>` - uploads a file to a bucket(file-path can be relative or absolute, file-name is derived from file-path).
- `file download <bucket-name> <file-name> <destination-path>` - download a file from a bucket.

### Examples
- `akavecli bucket create foo --node-address="localhost:5000"` - creates bucket named `foo` on a node with address `localhost:5000`
- `akavecli file upload foo ~/example.png --node-address="localhost:5000"` - uploads a file `example.png` from user's home folder to bucket `foo`
- `akavecli file download foo example.png . --node-address="localhost:5000" --maxConcurrency=20` - downloads a file `example.png` from akavenode at localhost:5000 from bucket `foo` to current folder using at most 20 simulteneous requests, result path is `./example.png`
