package subcmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/segmentio/encoding/json"
	"github.com/segmentio/kubeapply/pkg/cluster/diff"
	"github.com/spf13/cobra"
)

var kdiffCmd = &cobra.Command{
	Use:    "kdiff [old path] [new path]",
	Short:  "kdiff is used for generating structured output from kubectl diff; for internal use only",
	Hidden: true,
	Args:   cobra.MinimumNArgs(1),
	RunE:   kdiffRun,
}

type kdiffEnv struct{}

var kdiffEnvValues kdiffEnv

func init() {
	RootCmd.AddCommand(kdiffCmd)
}

func kdiffRun(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return errors.New("Expected exactly two arguments")
	}

	var shortDiff bool
	var err error
	if len(args) == 3 {
		shortDiff, err = strconv.ParseBool(args[2])
		if err != nil {
			return err
		}
	}

	results, err := diff.DiffKube(args[0], args[1], shortDiff)
	if err != nil {
		return err
	}

	wrappedResults := diff.Results{
		Results: results,
	}

	jsonBytes, err := json.MarshalIndent(wrappedResults, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonBytes))

	return nil
}

func envIsTrue(envName string) bool {
	return strings.ToLower(os.Getenv(envName)) == "true"
}
