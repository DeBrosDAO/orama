package cmdmeta

import (
	"maps"

	"github.com/spf13/cobra"
)

// listedKey marks a command that is hidden from `orama --help` but still part
// of the command reference.
const listedKey = "orama.listed"

// MarkListed declares cmd hidden from help yet documented. The maintainer group
// and the operator groups that `orama setup`, `status` and `upgrade` are
// replacing are hidden so a newcomer's help is short, but every command in them
// works and has a page in the reference and an end-to-end test. A deprecated
// alias is hidden and not listed: it is documented where its target is.
func MarkListed(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[listedKey] = "true"
	return cmd
}

// IsListed reports whether cmd was declared with MarkListed.
func IsListed(cmd *cobra.Command) bool {
	return cmd.Annotations[listedKey] == "true"
}

// HiddenAlias returns a hidden command that runs target: the same flags, the
// same handler, the same annotations (so a node-local command stays node-local).
//
// A systemd unit that is already installed on a node runs the path a release
// before this one gave it. When a command moves, the old path stays as a hidden
// alias so the unit keeps working until its next upgrade rewrites it.
//
// Cobra stores one parent per command, so the alias is a command of its own. It
// shares target's flags, which parse into the same variables: only one of the
// two runs in a process. Call it after target's flags are declared.
func HiddenAlias(target *cobra.Command) *cobra.Command {
	alias := &cobra.Command{
		Use:     target.Use,
		Short:   target.Short,
		Long:    target.Long,
		Example: target.Example,
		Args:    target.Args,
		Hidden:  true,

		PersistentPreRun:  target.PersistentPreRun,
		PersistentPreRunE: target.PersistentPreRunE,
		PreRun:            target.PreRun,
		PreRunE:           target.PreRunE,
		Run:               target.Run,
		RunE:              target.RunE,
		PostRun:           target.PostRun,
		PostRunE:          target.PostRunE,

		SilenceUsage:  target.SilenceUsage,
		SilenceErrors: target.SilenceErrors,
	}
	if target.Annotations != nil {
		alias.Annotations = maps.Clone(target.Annotations)
	}
	alias.SetFlagErrorFunc(target.FlagErrorFunc())
	alias.Flags().AddFlagSet(target.Flags())
	alias.PersistentFlags().AddFlagSet(target.PersistentFlags())
	return alias
}
