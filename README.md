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
- `bucket create` - Creates a new bucket.
  - Required arguments:
    - `bucket-name`: The name of the bucket to be created.
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli bucket create my-bucket --node-address="localhost:5000"
    ```

- `bucket view` - Views details of a specific bucket.
  - Required arguments:
    - `bucket-name`: The name of the bucket to view.
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli bucket view my-bucket --node-address="localhost:5000"
    ```

- `bucket list` - Lists all buckets.
  - Required arguments:
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli bucket list --node-address="localhost:5000"
    ```

#### File commands
- `file list` - Lists all files in a bucket.
  - Required arguments:
    - `bucket-name`: The name of the bucket to list files from.
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli file list my-bucket --node-address="localhost:5000"
    ```

- `file info` - Provides information about a file.
  - Required arguments:
    - `bucket-name`: The name of the bucket containing the file.
    - `file-name`: The name of the file to get information about.
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli file info my-bucket my-file.txt --node-address="localhost:5000"
    ```

- `file upload` - Uploads a file to a bucket.
  - Required arguments:
    - `bucket-name`: The name of the bucket to upload the file to.
    - `file-path`: The path to the file to be uploaded. It can be relative or absolute.
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli file upload my-bucket /path/to/my-file.txt --node-address="localhost:5000"
    ```

- `file download` - Downloads a file from a bucket.
  - Required arguments:
    - `bucket-name`: The name of the bucket containing the file.
    - `file-name`: The name of the file to be downloaded.
    - `destination-path`: The path where the file will be downloaded to.
    - `--node-address`: Address of the node you're connecting to.
  - Example:
    ```
    akavecli file download my-bucket my-file.txt /path/to/destination/ --node-address="localhost:5000"
    ```
