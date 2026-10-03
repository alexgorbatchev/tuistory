package app

import (
	"strings"

	"github.com/spf13/cobra"
)

// LaunchCommand preserves arguments after --, while launch's positional form
// explicitly accepts shell source. The label also preserves argv on restart.
func LaunchCommand(cmd *cobra.Command, args []string) (string, []string, string) {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 && dash < len(args) {
		argv := args[dash:]
		quoted := make([]string, len(argv))
		for i, arg := range argv {
			quoted[i] = shellQuote(arg)
		}
		return argv[0], argv[1:], strings.Join(quoted, " ")
	}
	label := strings.Join(args, " ")
	return "sh", []string{"-c", label}, label
}
