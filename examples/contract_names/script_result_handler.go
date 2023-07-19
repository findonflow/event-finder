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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bjartek/overflow"
	fbs "github.com/onflow/flow-batch-scan"
	"github.com/onflow/flow-go/utils/io"
	"github.com/rs/zerolog"
)

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
