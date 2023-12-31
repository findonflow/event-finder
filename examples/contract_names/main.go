// Copyright 2023 Dapper Labs, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/bjartek/overflow"
	"github.com/onflow/cadence"
	fbs "github.com/onflow/flow-batch-scan"
	scanner "github.com/onflow/flow-batch-scan"
	"github.com/onflow/flow-batch-scan/candidates"
	"github.com/onflow/flow-batch-scan/client"
	"github.com/onflow/flow-go-sdk"
	"github.com/onflow/flow-go/utils/io"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Script is the Cadence script to be executed on each batch of accounts.
// The output of the script can in general be any cadence value.
// In this case we want to know which address has which contract names deployed,
// so an array of structs is returned.
//
//go:embed get_contract_names.cdc
var Script string

func main() {
	fmt.Println(Script)
	// Create a logger to output nice looking output to the console.
	// Some output can also be found on the Debug level.
	log.Logger = log.
		Output(zerolog.ConsoleWriter{Out: os.Stderr}).
		Level(zerolog.InfoLevel)

	// Create a client to connect to the Flow network.
	// Any access api would work.
	// This uses `client.Client` from the flow-batch-scan package, which has some rate limits already set,
	// and a timeout, in case the network is not responding.
	flowClient, err := client.NewClient(
		"access.devnet.nodes.onflow.org:9000",
		client.WithLog(log.Logger),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create client")
	}
	defer func() {
		err := flowClient.Close()
		if err != nil {
			log.Error().Err(err).Msg("failed to close client")
		}
	}()

	// Candidate scanners are responsible for catching any accounts
	// that might have changed since the block the full scan began.
	// In this case we know that an accounts contracts have changed,
	// if the flow.AccountContractUpdated event was emitted.
	// we just need to decode the event to get the address of the account from it.
	candidateScanners := []candidates.CandidateScanner{
		candidates.NewAuthorizerCandidatesScanner(log.Logger),
		candidates.NewEventCandidatesScanner(
			"flow.AccountContractUpdated",
			func(event cadence.Event) (flow.Address, error) {
				// get the address from the event.
				return flow.BytesToAddress(event.Fields[0].(cadence.Address).Bytes()), nil
			},
			log.Logger,
		),
	}

	// This is the result handler, that will handle the results from the scripts.
	scriptResultHandler := NewScriptResultHandler(log.Logger)

	// simple scripts can have a bigger batch size.
	// because they are faster to execute and use less computation.
	batchSize := 200

	config := scanner.DefaultConfig().
		WithScript([]byte(Script)).
		WithCandidateScanners(candidateScanners).
		WithScriptResultHandler(scriptResultHandler).
		WithBatchSize(batchSize).
		WithChainID(flow.Testnet).
		WithLogger(log.Logger).
		// false is actually the default.
		// This means that once the full scan is done the scanner will stop.
		// At which point the results will be complete at the `result.LatestScannedBlockHeight`
		WithContinuousScan(false)

	// The scanner Will start scanning from the latest sealed block.
	// It will run a full scan, that will switch to a newer reference block every so often.
	// It will also run an incremental scanner, that will catch any changes that happened since the full scan started
	// using the `candidateScanners`.
	scan := scanner.NewScanner(
		flowClient,
		config,
	)

	// Start the scanner.
	result, err := scan.Scan(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("scanner failed")
	}
	// `result.ScanIsComplete` would be false if not all accounts were scanned.
	// Or if the incremental scanner missed any changes.
	log.Info().
		Uint64("scan_complete_at_block", result.LatestScannedBlockHeight).
		Bool("result_accurate", result.ScanIsComplete).
		Msg("scanner finished")
}

type Record struct {
	Contracts   []string
	BlockHeight uint64
}

type scriptResultHandler struct {
	logger zerolog.Logger
}

// NewScriptResultHandler is a simple result handler that prints the results to the log.
func NewScriptResultHandler(
	logger zerolog.Logger,
) fbs.ScriptResultHandler {
	h := &scriptResultHandler{
		logger: logger,
	}
	return h
}

func (r *scriptResultHandler) Handle(batch fbs.ProcessedAddressBatch) error {

	//read as overflow value
	value, err := overflow.CadenceValueToJsonString(batch.Result)
	if err != nil {
		r.logger.Error().Err(err).Msg("cadence value convert")
		return nil
	}

	if strings.TrimSpace(value) == "" {
		return nil
	}

	var contracts []Contract
	err = json.Unmarshal([]byte(value), &contracts)
	if err != nil {
		r.logger.Error().Err(err).Str("input", value).Msg("marshal to contract")
		return nil
	}
	for _, c := range contracts {
		prefix := strings.TrimPrefix(c.Address, "0x")
		for name, body := range c.Contracts {
			bodyBytes := []byte(body)
			fileName := fmt.Sprintf("result/A.%s.%s.cdc", prefix, name)
			err := io.WriteFile(fileName, bodyBytes)
			if err != nil {
				return err
			}

			//	r.logger.Info().Msg(fileName)
		}
	}
	return nil
}

type Contract struct {
	Address   string
	Contracts map[string]string
}
