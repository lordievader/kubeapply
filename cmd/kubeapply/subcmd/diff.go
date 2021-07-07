package subcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/briandowns/spinner"
	"github.com/segmentio/kubeapply/pkg/cluster"
	"github.com/segmentio/kubeapply/pkg/cluster/diff"
	"github.com/segmentio/kubeapply/pkg/cluster/kube"
	"github.com/segmentio/kubeapply/pkg/config"
	"github.com/segmentio/kubeapply/pkg/util"
	"github.com/segmentio/kubeapply/pkg/version"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	spinnerCharSet  = 32
	spinnerDuration = 200 * time.Millisecond
)

var diffCmd = &cobra.Command{
	Use:   "diff [cluster configs]",
	Short: "diff shows the difference between the local configs and the API state",
	Args:  cobra.MinimumNArgs(1),
	RunE:  diffRun,
}

type diffFlags struct {
	// Expand before running diff.
	expand bool

	// Path to kubeconfig. If unset, tries to fetch from the environment.
	kubeConfig string

	// Whether to just run "kubectl diff" with the default output options
	simpleOutput bool

	// Run operatation in just a subset of the subdirectories of the expanded configs
	// (typically maps to namespace). Globs are allowed. If unset, considers all configs.
	subpaths []string
}

var diffFlagValues diffFlags

func init() {
	diffCmd.Flags().BoolVar(
		&diffFlagValues.expand,
		"expand",
		false,
		"Expand before running diff",
	)
	diffCmd.Flags().StringVar(
		&diffFlagValues.kubeConfig,
		"kubeconfig",
		"",
		"Path to kubeconfig",
	)
	diffCmd.Flags().BoolVar(
		&diffFlagValues.simpleOutput,
		"simple-output",
		false,
		"Run with simple output",
	)
	diffCmd.Flags().StringArrayVar(
		&diffFlagValues.subpaths,
		"subpath",
		[]string{},
		"Diff for expanded configs in the provided subpath(s) only",
	)

	RootCmd.AddCommand(diffCmd)
}

func diffRun(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	for _, arg := range args {
		paths, err := filepath.Glob(arg)
		if err != nil {
			return err
		}

		for _, path := range paths {
			if err := diffClusterPath(ctx, path); err != nil {
				return err
			}
		}
	}

	return nil
}

func diffClusterPath(ctx context.Context, path string) error {
	clusterConfig, err := config.LoadClusterConfig(path, "")
	if err != nil {
		return err
	}
	if err := clusterConfig.CheckVersion(version.Version); err != nil {
		return err
	}

	if diffFlagValues.expand {
		if err := expandCluster(ctx, clusterConfig, false); err != nil {
			return err
		}
	}

	log.Infof("Diffing cluster %s", clusterConfig.DescriptiveName())

	ok, err := util.DirExists(clusterConfig.ExpandedPath)
	if err != nil {
		return err
	} else if !ok {
		return fmt.Errorf(
			"Expanded path %s does not exist",
			clusterConfig.ExpandedPath,
		)
	}

	kubeConfig := diffFlagValues.kubeConfig

	if kubeConfig == "" {
		kubeConfig = os.Getenv("KUBECONFIG")
		if kubeConfig == "" {
			return errors.New("Must either set --kubeconfig flag or KUBECONFIG env variable")
		}
	}

	matches := kube.KubeconfigMatchesCluster(kubeConfig, clusterConfig.Cluster)
	if !matches {
		return fmt.Errorf(
			"Kubeconfig in %s does not appear to reference cluster %s",
			kubeConfig,
			clusterConfig.Cluster,
		)
	}

	clusterConfig.KubeConfigPath = kubeConfig
	clusterConfig.Subpaths = diffFlagValues.subpaths

	results, rawDiffs, err := execDiff(ctx, clusterConfig, diffFlagValues.simpleOutput)
	if err != nil {
		log.Errorf("Error running diff: %+v", err)
		log.Info(
			"Try re-running with --simple-output, then with --debug to see verbose output. Note that diffs will not work if target namespace(s) don't exist yet.",
		)
		return err
	}

	if results != nil {
		diff.PrintFull(results)
	} else {
		log.Infof("Raw diff results:\n%s", rawDiffs)
	}

	return nil
}

func execDiff(
	ctx context.Context,
	clusterConfig *config.ClusterConfig,
	simpleOutput bool,
) ([]diff.Result, string, error) {
	log.Info("Generating diff against versions in Kube API")

	spinnerObj := spinner.New(
		spinner.CharSets[spinnerCharSet],
		spinnerDuration,
		spinner.WithWriter(os.Stderr),
		spinner.WithHiddenCursor(true),
	)
	spinnerObj.Prefix = "Running: "

	kubeClient, err := cluster.NewKubeClusterClient(
		ctx,
		&cluster.ClusterClientConfig{
			CheckApplyConsistency: false,
			ClusterConfig:         clusterConfig,
			Debug:                 debug,
			SpinnerObj:            spinnerObj,
			// TODO: Make locking an option
			UseLocks: false,
		},
	)
	if err != nil {
		return nil, "", err
	}
	defer kubeClient.Close()

	// If a cluster UID was provided, verify that the cluster we are operating on
	// has this same UID. Otherwise bail.
	if clusterConfig.UID != "" {
		actualUID, err := kubeClient.GetNamespaceUID(ctx, "kube-system")
		if err != nil {
			return nil, "", err
		}

		if clusterConfig.UID != actualUID {
			return nil, "", fmt.Errorf(
				"Kubeapply config does not match this cluster (wrong kube context?): kube-system uids do not match (%s!=%s)",
				clusterConfig.UID,
				actualUID,
			)
		}
	}

	if simpleOutput {
		rawResults, err := kubeClient.Diff(
			ctx,
			clusterConfig.AbsSubpaths(),
			clusterConfig.ServerSideApply,
		)
		return nil, string(rawResults), err
	}

	results, err := kubeClient.DiffStructured(
		ctx,
		clusterConfig.AbsSubpaths(),
		clusterConfig.ServerSideApply,
		"",
	)
	return results, "", err
}
