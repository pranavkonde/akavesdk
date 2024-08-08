// Copyright (C) 2024 Akave
// See LICENSE for copying information.

package sdk

import (
	"context"
	"io"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	chunker "github.com/ipfs/boxo/chunker"
	"github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	"github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/sync"
)

// FileDAG represents the result of a chunk stream ReadAll operation.
type FileDAG struct {
	RootCID cid.Cid
	Chunks  []FileChunk
}

// CalculateDAG calculates the DAG of a file.
func CalculateDAG(ctx context.Context, reader io.Reader, chunkSize int64) (FileDAG, error) {
	cidBuilder, err := merkledag.PrefixForCidVersion(1)
	if err != nil {
		return FileDAG{}, err
	}

	splitter := chunker.NewSizeSplitter(io.NopCloser(reader), chunkSize)
	batching := sync.MutexWrap(datastore.NewMapDatastore())
	store := blockstore.NewBlockstore(batching)
	dagServ := merkledag.NewDAGService(blockservice.New(store, offline.Exchange(store)))

	params := helpers.DagBuilderParams{
		Maxlinks:   1024,
		RawLeaves:  false, // 1048590 pb encoded ,1048576 - raw; merkledag.ProtoNode merkledag.RawNode
		CidBuilder: cidBuilder,
		Dagserv:    dagServ,
		NoCopy:     false,
	}

	db, err := params.New(splitter)
	if err != nil {
		return FileDAG{}, err
	}

	rootNode, err := balanced.Layout(db)
	if err != nil {
		return FileDAG{}, err
	}

	var chunks = make([]FileChunk, 0)
	if len(rootNode.Links()) == 0 {
		// Single chunk case, the root node itself is the chunk
		node, err := dagServ.Get(ctx, rootNode.Cid())
		if err != nil {
			return FileDAG{}, err
		}
		chunks = append(chunks, FileChunk{
			CID:  rootNode.Cid().String(),
			Size: uint64(len(node.RawData())),
			Data: node.RawData(),
		})
	} else {
		for _, l := range rootNode.Links() {
			node, err := dagServ.Get(ctx, l.Cid)
			if err != nil {
				return FileDAG{}, err
			}
			chunks = append(chunks, FileChunk{
				CID:  l.Cid.String(),
				Size: l.Size,
				Data: node.RawData(),
			})
		}
	}
	return FileDAG{
		RootCID: rootNode.Cid(),
		Chunks:  chunks,
	}, nil
}

func chunkByCID(chunks []FileChunk, cid string) (FileChunk, bool) {
	for _, v := range chunks {
		if v.CID == cid {
			return v, true
		}
	}
	return FileChunk{}, false
}
