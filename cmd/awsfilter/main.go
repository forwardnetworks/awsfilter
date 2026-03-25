package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/forwardnetworks/awsfilter/internal/api"
	"github.com/forwardnetworks/awsfilter/internal/app"
	"github.com/forwardnetworks/awsfilter/internal/monitor"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	v := viper.New()
	v.SetEnvPrefix("AWSFILTER")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	v.SetDefault("api-prefix", "/api")
	v.SetDefault("timeout", 10*time.Minute)
	v.SetDefault("wait-for-state", "PROCESSED")
	v.SetDefault("poll-interval", 10*time.Second)

	cmd := &cobra.Command{
		Use:   "awsfilter",
		Short: "Download the latest Forward snapshot and keep only collected-account AWS compute footprint",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := app.Config{
				Host:                 v.GetString("host"),
				Username:             v.GetString("username"),
				Password:             v.GetString("password"),
				NetworkID:            v.GetString("network-id"),
				Output:               v.GetString("output"),
				APIPrefix:            v.GetString("api-prefix"),
				Insecure:             v.GetBool("insecure"),
				Timeout:              v.GetDuration("timeout"),
				Import:               v.GetBool("import"),
				ImportNote:           v.GetString("import-note"),
				DeleteSourceSnapshot: v.GetBool("delete-source-snapshot"),
			}
			summary, err := app.Run(context.Background(), cfg)
			if err != nil {
				return err
			}
			return emitJSON(summary)
		},
	}

	bindCommonFlags(v, cmd.PersistentFlags())
	bindRunFlags(v, cmd.Flags())
	cmd.AddCommand(newStatusCommand(v), newWaitCommand(v))
	return cmd
}

func newStatusCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show snapshot status for a network or a specific snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(v)
			if err != nil {
				return err
			}
			result, err := monitor.Status(
				context.Background(),
				client,
				v.GetString("network-id"),
				v.GetString("snapshot-id"),
			)
			if err != nil {
				return err
			}
			return emitJSON(result)
		},
	}
	cmd.Flags().String("snapshot-id", "", "optional snapshot ID to filter status output")
	mustBind(v, cmd.Flags(), "snapshot-id")
	return cmd
}

func newWaitCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for a snapshot to reach a desired state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(v)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), v.GetDuration("timeout"))
			defer cancel()
			result, err := monitor.Wait(
				ctx,
				client,
				v.GetString("network-id"),
				v.GetString("snapshot-id"),
				v.GetString("wait-for-state"),
				v.GetDuration("poll-interval"),
			)
			if err != nil {
				return err
			}
			return emitJSON(result)
		},
	}
	cmd.Flags().String("snapshot-id", "", "snapshot ID to monitor")
	cmd.Flags().String("wait-for-state", "PROCESSED", "desired snapshot state")
	cmd.Flags().Duration("poll-interval", 10*time.Second, "poll interval while waiting")
	mustBind(v, cmd.Flags(), "snapshot-id")
	mustBind(v, cmd.Flags(), "wait-for-state")
	mustBind(v, cmd.Flags(), "poll-interval")
	_ = cmd.MarkFlagRequired("snapshot-id")
	return cmd
}

func bindCommonFlags(v *viper.Viper, flags *pflag.FlagSet) {
	flags.String("host", "", "Forward base URL, for example https://fwd.app")
	flags.String("username", "", "Forward username")
	flags.String("password", "", "Forward password")
	flags.String("network-id", "", "Forward network ID")
	flags.String("api-prefix", "/api", "API prefix")
	flags.Bool("insecure", false, "skip TLS certificate verification")
	flags.Duration("timeout", 10*time.Minute, "HTTP timeout")
	mustBind(v, flags, "host")
	mustBind(v, flags, "username")
	mustBind(v, flags, "password")
	mustBind(v, flags, "network-id")
	mustBind(v, flags, "api-prefix")
	mustBind(v, flags, "insecure")
	mustBind(v, flags, "timeout")
}

func bindRunFlags(v *viper.Viper, flags *pflag.FlagSet) {
	flags.String("output", "", "output zip path; defaults to ./network-<id>-snapshot-<id>-collected-compute-only.zip")
	flags.Bool("import", false, "import the filtered snapshot back into the network")
	flags.String("import-note", "Imported by awsfilter: collected-account AWS compute footprint only", "note to attach when importing the filtered snapshot")
	flags.Bool("delete-source-snapshot", false, "delete the source latest snapshot after a successful import")
	mustBind(v, flags, "output")
	mustBind(v, flags, "import")
	mustBind(v, flags, "import-note")
	mustBind(v, flags, "delete-source-snapshot")
}

func mustBind(v *viper.Viper, flags *pflag.FlagSet, name string) {
	if err := v.BindPFlag(name, flags.Lookup(name)); err != nil {
		panic(err)
	}
	if err := v.BindEnv(name); err != nil {
		panic(err)
	}
}

func newAPIClient(v *viper.Viper) (*api.Client, error) {
	return api.NewClient(
		v.GetString("host"),
		v.GetString("api-prefix"),
		v.GetString("username"),
		v.GetString("password"),
		v.GetBool("insecure"),
		v.GetDuration("timeout"),
	)
}

func emitJSON(value any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
