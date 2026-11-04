// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"sync"

	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/rice"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

const (
	// orderedBlockSize bounds hash work and the buffers retained before encoding.
	orderedBlockSize = 1 << 16
	// maxLogicalEncoderShards bounds stable bucket partitions independently of CPU count.
	maxLogicalEncoderShards = 32
)

// blockResult carries one hashed block until all bucket shards encode it in order.
type blockResult struct {
	err     error                           // Hash or cancellation error for this block.
	buckets []uint32                        // Bucket ID for each account offset in the block.
	offsets []uint16                        // Stable offsets grouped by encoder shard.
	index   uint64                          // Global sequence number used to restore block order.
	shards  [maxLogicalEncoderShards]uint32 // Number of grouped offsets assigned to each shard.
}

// blockJob transfers a buffer pair from the coordinator to one hash worker.
type blockJob struct {
	buckets []uint32 // Reusable bucket-ID buffer assigned before dispatch.
	offsets []uint16 // Reusable shard-order buffer assigned before dispatch.
	index   uint64   // Global block sequence number.
}

// encoderJob gives one shard its contiguous portion of an ordered hash block.
type encoderJob struct {
	block   *blockResult    // Hash results shared read-only by the shard workers.
	done    *sync.WaitGroup // Signals completion of the shard's portion.
	firstID uint64          // SteamID64 corresponding to block offset zero.
	start   uint32          // First grouped offset owned by this shard.
	count   uint32          // Number of grouped offsets owned by this shard.
}

// buildOrderedSegment hashes bounded blocks once and encodes them in account order.
func buildOrderedSegment(
	c Config,
	entries uint64,
	bucketCount uint32,
	hashWorkers, encodeWorkers int,
	built *builtSegment,
) error {
	shardCount := 1 << min(int(c.PrefixBits), 5)
	shardCount = min(shardCount, int(bucketCount))
	blockCount := (entries + orderedBlockSize - 1) / orderedBlockSize
	encoder, err := rice.NewEncoder(c.RiceK)
	if err != nil {
		return err
	}

	encoders := make([]rice.Encoder, bucketCount)
	for i := range encoders {
		encoders[i] = encoder
	}

	counts := make([]uint64, bucketCount)
	lastAccount := make([]uint32, bucketCount)
	// #nosec G115 - the validated range fits account IDs.
	baseAccount := uint32(c.Range.Min - steamid.SteamIDBase)
	dayZ := c.Algorithm == segment.DayZ
	// #nosec G115 - shardCount is at most maxLogicalEncoderShards.
	shifts := c.PrefixBits - uint8(bits.TrailingZeros(uint(shardCount)))
	// #nosec G115 - shardCount is a positive power of two.
	shardLimit := uint32(shardCount)
	encoderJobs := make(chan encoderJob, shardCount)
	var encoderGroup sync.WaitGroup

	for range encodeWorkers {
		encoderGroup.Go(func() {
			for job := range encoderJobs {
				for i := job.start; i < job.start+job.count; i++ {
					offset := job.block.offsets[i]
					bucket := job.block.buckets[offset]
					// #nosec G115 - within validated range.
					account := uint32(job.firstID + uint64(offset) - steamid.SteamIDBase)
					gap := account - baseAccount
					if counts[bucket] > 0 {
						gap = account - lastAccount[bucket] - 1
					}

					encoders[bucket].Append(gap)
					counts[bucket]++
					lastAccount[bucket] = account
				}

				job.done.Done()
			}
		})
	}
	defer func() {
		close(encoderJobs)
		encoderGroup.Wait()
	}()

	freeBlocks := make(chan []uint32, hashWorkers+1)
	freeOffsets := make(chan []uint16, hashWorkers+1)
	blockLength := min(uint64(orderedBlockSize), entries)
	for range hashWorkers + 1 {
		freeBlocks <- make([]uint32, int(blockLength))
		freeOffsets <- make([]uint16, int(blockLength))
	}

	jobs := make(chan blockJob, hashWorkers)
	results := make(chan blockResult, hashWorkers)
	var group sync.WaitGroup

	for range hashWorkers {
		group.Go(func() {
			for job := range jobs {
				block := job.index
				buckets := job.buckets
				offsets := job.offsets
				firstID := c.Range.Min + block*orderedBlockSize
				count := min(uint64(orderedBlockSize), entries-block*orderedBlockSize)
				buckets = buckets[:count]
				offsets = offsets[:count]
				var decimal dayZDecimalCursor
				if dayZ {
					decimal.reset(firstID)
				}

				var blockErr error
				var shardCounts [maxLogicalEncoderShards]uint32
				for offset := range count {
					if offset&1023 == 0 {
						if err := c.Context.Err(); err != nil {
							blockErr = err
							break
						}
					}

					var bucketID uint32
					if dayZ {
						bucketID = decimal.nextBucket(c.PrefixBits)
					} else {
						bucketID = bucket(c.Algorithm, firstID+offset, c.PrefixBits)
					}

					buckets[offset] = bucketID
					shard := bucketID >> shifts
					if shard >= shardLimit {
						shard = shardLimit - 1
					}

					shardCounts[shard]++
				}

				var starts [maxLogicalEncoderShards]uint32
				var writeAt [maxLogicalEncoderShards]uint32
				for shard := 1; shard < shardCount; shard++ {
					starts[shard] = starts[shard-1] + shardCounts[shard-1]
				}

				writeAt = starts
				if blockErr == nil {
					for offset, bucketID := range buckets {
						shard := bucketID >> shifts
						if shard >= shardLimit {
							shard = shardLimit - 1
						}

						at := writeAt[shard]
						// #nosec G115 - block offsets fit uint16.
						offsets[at] = uint16(offset)
						writeAt[shard]++
					}
				}

				results <- blockResult{
					index:   block,
					buckets: buckets,
					offsets: offsets,
					shards:  shardCounts,
					err:     blockErr,
				}
			}
		})
	}

	go func() {
		for block := range blockCount {
			if c.Context.Err() != nil {
				break
			}

			// Reserve buffers before dispatch so queued jobs cannot starve an earlier ordered block.
			buckets := <-freeBlocks
			offsets := <-freeOffsets
			if c.Context.Err() != nil {
				freeBlocks <- buckets
				freeOffsets <- offsets
				break
			}
			jobs <- blockJob{index: block, buckets: buckets, offsets: offsets}
		}

		close(jobs)
		group.Wait()
		close(results)
	}()

	progress := newProgress("build", entries, hashWorkers, c.OnProgress, c.SegmentIndex, c.SegmentCount)
	pending := make(map[uint64]blockResult, hashWorkers*2)
	nextBlock := uint64(0)
	var pipelineErr error
	var blockDone sync.WaitGroup

	for result := range results {
		pending[result.index] = result
		for {
			current, ok := pending[nextBlock]
			if !ok {
				break
			}

			delete(pending, nextBlock)
			if current.err != nil && pipelineErr == nil {
				pipelineErr = current.err
			}

			if pipelineErr == nil {
				firstID := c.Range.Min + nextBlock*orderedBlockSize
				if err := c.Context.Err(); err != nil {
					pipelineErr = err
				} else {
					blockDone.Add(shardCount)
					start := uint32(0)

					for shard := range shardCount {
						count := current.shards[shard]
						encoderJobs <- encoderJob{
							firstID: firstID,
							start:   start,
							count:   count,
							block:   &current,
							done:    &blockDone,
						}
						start += count
					}

					blockDone.Wait()

					progress.add(uint64(len(current.buckets)))
				}
			}

			freeBlocks <- current.buckets[:cap(current.buckets)]
			freeOffsets <- current.offsets[:cap(current.offsets)]
			nextBlock++
		}
	}

	if pipelineErr != nil {
		return pipelineErr
	}
	if err := c.Context.Err(); err != nil {
		return err
	}
	if nextBlock != blockCount {
		return ErrOrderedPipelineIncomplete
	}

	return writeEncodedIndex(c, encoders, counts, entries, bucketCount, built)
}

// writeEncodedIndex writes ordered per-bucket streams using the unchanged v1 segment layout.
func writeEncodedIndex(
	c Config,
	encoders []rice.Encoder,
	counts []uint64,
	entries uint64,
	buckets uint32,
	built *builtSegment,
) (err error) {
	dir := make([]uint32, buckets+1)
	payloadHash := sha256.New()
	payloadOffset := uint64(segment.HeaderSize) + uint64(buckets+1)*4
	if payloadOffset > math.MaxInt64 {
		return ErrPayloadOffsetRange
	}

	payloadSize := uint64(0)
	var writeProgress *progress
	if c.OnProgress != nil {
		writeProgress = newProgress("write", uint64(buckets), 1, c.OnProgress, c.SegmentIndex, c.SegmentCount)
	}

	// Precompute payload metadata in memory so the final file can be written once in order.
	for bucket := range buckets {
		if err := c.Context.Err(); err != nil {
			return err
		}
		if payloadSize > math.MaxUint32 {
			return ErrPayloadDirectoryLimit
		}

		dir[bucket] = uint32(payloadSize)
		if counts[bucket] > math.MaxUint32 {
			return ErrBucketCountLimit
		}

		var countBytes [4]byte
		binary.LittleEndian.PutUint32(countBytes[:], uint32(counts[bucket]))
		encoded := encoders[bucket].Bytes()
		bucketSize := uint64(len(encoded)) + 4
		if bucketSize > limits.MaxBucketByteSize {
			return fmt.Errorf("%w: bucket %d size %d, limit %d", ErrBucketSizeLimit, bucket, bucketSize, limits.MaxBucketByteSize)
		}

		maxPayloadSize := uint64(limits.MaxSegmentFileSize) - payloadOffset
		if payloadSize > maxPayloadSize || bucketSize > maxPayloadSize-payloadSize {
			return ErrSegmentPayloadLimit
		}
		_, _ = payloadHash.Write(countBytes[:])
		_, _ = payloadHash.Write(encoded)

		payloadSize += bucketSize
		if writeProgress != nil {
			if uint64(bucket)+1 >= writeProgress.processed.Load()+writeProgress.chunk {
				writeProgress.add(uint64(bucket) + 1 - writeProgress.processed.Load())
			}
		}
	}
	if writeProgress != nil {
		writeProgress.add(uint64(buckets) - writeProgress.processed.Load())
	}
	if payloadSize > math.MaxUint32 {
		return ErrPayloadDirectoryLimit
	}

	dir[buckets] = uint32(payloadSize)

	dirBytes := make([]byte, len(dir)*4)
	for i, value := range dir {
		binary.LittleEndian.PutUint32(dirBytes[i*4:], value)
	}
	h := segment.Header{
		Algorithm:       c.Algorithm,
		PrefixBits:      c.PrefixBits,
		RiceK:           c.RiceK,
		Base:            steamid.SteamIDBase,
		Min:             c.Range.Min,
		Max:             c.Range.Max,
		EntryCount:      entries,
		BucketCount:     buckets,
		DirectoryOffset: segment.HeaderSize,
		DirectorySize:   uint64(len(dirBytes)),
		PayloadOffset:   payloadOffset,
		PayloadSize:     payloadSize,
		DirectorySHA256: segment.DirectoryHash(dirBytes),
	}

	copy(h.PayloadSHA256[:], payloadHash.Sum(nil))
	header, err := h.MarshalBinary()
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(c.Output), ".ivz-*.tmp")
	if err != nil {
		return err
	}

	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()

	fileHash := sha256.New()
	writer := bufio.NewWriterSize(io.MultiWriter(tmp, fileHash), segmentPayloadBufferSize)
	if _, err = writer.Write(header); err != nil {
		return err
	}
	if _, err = writer.Write(dirBytes); err != nil {
		return err
	}

	for bucket := range buckets {
		if counts[bucket] > math.MaxUint32 {
			return ErrBucketCountLimit
		}

		var countBytes [4]byte
		binary.LittleEndian.PutUint32(countBytes[:], uint32(counts[bucket]))
		if _, err = writer.Write(countBytes[:]); err != nil {
			return err
		}
		if _, err = writer.Write(encoders[bucket].Bytes()); err != nil {
			return err
		}
	}

	if err = writer.Flush(); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, c.Output); err != nil {
		return err
	}

	built.SHA256 = hex.EncodeToString(fileHash.Sum(nil))
	built.Size = payloadOffset + payloadSize

	return nil
}
