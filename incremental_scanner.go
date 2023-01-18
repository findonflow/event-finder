package lib

import (
	"context"
	_ "embed"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onflow/flow-go-sdk"
	"github.com/rs/zerolog"

	"github.com/onflow/flow-batch-scan/candidates"
	"github.com/onflow/flow-batch-scan/client"
)

// IncrementalScannerBlockLag is the number of blocks the incremental scanner lag behind the latest block from
// GetLatestBlockHeader. This is to avoid most of the "retry for collection in finalized block" errors.
const IncrementalScannerBlockLag = 5

// IncrementalScannerMaxBlockGap is the maximum number of blocks that can scanned by the incremental scanner.
// If the gap is larger than this, the incremental scanner will request a full scan.
const IncrementalScannerMaxBlockGap = 100

type IncrementalScanner struct {
	*ComponentBase

	client client.Client

	addressBatchChan chan<- AddressBatch
	requestFullScan  chan<- uint64

	latestBlock        uint64
	latestHandledBlock atomic.Uint64
	batchSize          int

	blockCandidateScanners []candidates.CandidateScanner

	reporter StatusReporter
}

func NewIncrementalScanner(
	ctx context.Context,
	client client.Client,

	addressBatchChan chan<- AddressBatch,
	requestBatchChan chan<- uint64,

	startAtBlock uint64,
	batchSize int,

	blockCandidateScanners []candidates.CandidateScanner,

	reporter StatusReporter,
	logger zerolog.Logger,

) *IncrementalScanner {
	r := &IncrementalScanner{
		ComponentBase: NewComponent("incremental_scanner", logger),

		client:             client,
		addressBatchChan:   addressBatchChan,
		requestFullScan:    requestBatchChan,
		latestBlock:        startAtBlock,
		latestHandledBlock: atomic.Uint64{},
		batchSize:          batchSize,

		blockCandidateScanners: blockCandidateScanners,

		reporter: reporter,
	}

	go r.run(ctx)
	r.StartupDone()
	return r
}

func (r *IncrementalScanner) run(ctx context.Context) {
	next := time.After(0)
	for {
		select {
		case <-ctx.Done():
			r.Finish(ctx.Err())
			return
		case <-next:
			next = time.After(2 * time.Second)
			err := r.scanNewBlocks(ctx)
			if err != nil {
				r.Finish(err)
			}
		}
	}
}

func (r *IncrementalScanner) scanNewBlocks(ctx context.Context) error {
	header, err := r.client.GetLatestBlockHeader(ctx, true)
	if err != nil {
		r.Logger.Error().Err(err).Msg("Could not get latest block header.")
		return err
	}
	height := header.Height - IncrementalScannerBlockLag

	if height <= r.latestBlock {
		return nil
	}

	r.reporter.ReportIncrementalBlockDiff(height - r.latestBlock)

	if height-r.latestBlock > IncrementalScannerMaxBlockGap {
		r.Logger.Info().
			Uint64("latest_block", r.latestBlock).
			Uint64("current_block", height).
			Uint64("diff", height-r.latestBlock).
			Msg("skipping blocks and requesting batch")
		r.latestBlock = height
		r.requestFullScan <- r.latestBlock
		return nil
	}

	r.Logger.Info().
		Uint64("start", r.latestBlock+1).
		Uint64("end", height).
		Uint64("diff", height-r.latestBlock).
		Msg("processing block range")
	err = r.scanBlockRange(ctx, r.latestBlock+1, height)

	r.latestBlock = height
	return err
}

// scanBlockRange scans a range of blocks for any candidates for which a script should be run.
// start and end are inclusive.
func (r *IncrementalScanner) scanBlockRange(ctx context.Context, start uint64, end uint64) error {
	candidatesResult := r.runBlockCandidateScanners(ctx, start, end)
	if candidatesResult.Err() != nil {
		return candidatesResult.Err()
	}

	if len(candidatesResult.Addresses) == 0 {
		return nil
	}

	addresses := make([]flow.Address, 0, len(candidatesResult.Addresses))
	for address := range candidatesResult.Addresses {
		addresses = append(addresses, address)
	}

	if len(addresses) == 0 {
		return nil
	}
	r.Logger.
		Info().
		Int("count", len(addresses)).
		Uint64("start", start).
		Uint64("end", end).
		Msg("Found candidates in block range.")

	wg := sync.WaitGroup{}
	for i := 0; i < len(addresses); i += r.batchSize {
		startIndex := i
		endIndex := i + r.batchSize
		if endIndex > len(addresses) {
			endIndex = len(addresses)
		}
		wg.Add(1)
		r.addressBatchChan <- NewAddressBatch(
			addresses[startIndex:endIndex],
			end,
			func() {
				wg.Done()
			},
			nil,
		)
	}

	go func() {
		wg.Wait()
		r.latestHandledBlock.Store(end)
		r.reporter.ReportIncrementalBlockHeight(end)
	}()

	return nil
}

func (r *IncrementalScanner) runBlockCandidateScanners(ctx context.Context, start uint64, end uint64) candidates.CandidatesResult {
	results := make(chan candidates.CandidatesResult, len(r.blockCandidateScanners))
	defer close(results)

	for _, scanner := range r.blockCandidateScanners {
		go func(scanner candidates.CandidateScanner) {
			results <- scanner.Scan(ctx, r.client, candidates.BlockRange{Start: start, End: end})
		}(scanner)
	}

	return candidates.WaitForCandidateResults(results, len(r.blockCandidateScanners))
}

func (r *IncrementalScanner) LatestHandledBlock() uint64 {
	return r.latestHandledBlock.Load()
}