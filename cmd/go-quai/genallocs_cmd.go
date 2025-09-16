package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/spf13/cobra"
)

var (
	errNotEnoughArgs = errors.New("not enough arguments provided")
)

var generateAllocsCmd = &cobra.Command{
	Use:   "generate-allocs",
	Short: "creates the unlock schedule for the gen allocs",
	Long: `calculates unlock schedule based on the unlockSchedules defined in genesis_alloc.go
	it accepts an input json file that marshals into the GenesisAccount struct.
	it creates a sorted output json file that specifies the exact amount of Quai unlocked at each block for each account.
	this output file is RLP encoded and loaded into the node and included as part of the genesis block.
	`,
	RunE:                       generateAllocs,
	SilenceUsage:               true,
	SuggestionsMinimumDistance: 2,
	Example:                    `go-quai genallocs input_file_path.json output_file_path.json`,
	PreRunE:                    startCmdPreRun,
}

func generateAllocs(cmd *cobra.Command, args []string) error {
	// Ensure that input and output files are specified.
	if len(args) != 2 {
		log.Global.WithField("err", errNotEnoughArgs).Fatal("Unable to start")
		return errNotEnoughArgs
	}

	inputFilePath := filepath.Clean(args[0])
	outputFilePath := filepath.Clean(args[1])

	genesisAccounts, err := params.GenerateGenesisUnlocks(inputFilePath)
	if err != nil {
		return err
	}

	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	encoder := json.NewEncoder(outputFile)
	if err := encoder.Encode(genesisAccounts); err != nil {
		return err
	}

	return nil
}

func init() {
	rootCmd.AddCommand(generateAllocsCmd)

	for _, flagGroup := range utils.Flags {
		for _, flag := range flagGroup {
			utils.CreateAndBindFlag(flag, generateAllocsCmd)
		}
	}
}
