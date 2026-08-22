package cmd

import (
	"fmt"
	"time"

	"github.com/JustSteveKing/taskgo/internal/notify"
	"github.com/spf13/cobra"
)

func (a *app) newNotifyCommand() *cobra.Command {
	var (
		dryRun     bool
		force      bool
		printTimer bool
	)

	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Send desktop notifications for due and overdue tasks",
		Long: `Notify about work that is due today or already late.

Meant to be run on a timer rather than by hand. Each task is mentioned at most
once a day, so a task that stays overdue does not produce an identical popup
every run — but a task that becomes overdue later in the day still surfaces,
because the record is kept per task rather than per run.

Print ready-made systemd user units with --print-timer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if printTimer {
				a.printf("%s", systemdUnits())
				return nil
			}

			s, err := a.openStore()
			if err != nil {
				return err
			}
			now := time.Now()

			report, err := notify.Collect(s, now, force)
			if err != nil {
				return err
			}

			if a.jsonOut {
				return a.emitJSON(report)
			}

			if dryRun {
				a.printf("%s\n", describe(report))
				return nil
			}

			if report.Empty() {
				// Silence is the correct output for a timer that found
				// nothing; say so only when a human ran it directly.
				if report.Suppressed > 0 {
					a.printf("Nothing new (%d already notified today).\n", report.Suppressed)
				}
				return nil
			}

			if err := notify.Send(s, report, now); err != nil {
				return err
			}
			a.printf("Notified about %d task(s).\n", report.Count())
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be sent without sending it")
	cmd.Flags().BoolVar(&force, "force", false, "ignore the once-a-day record and notify anyway")
	cmd.Flags().BoolVar(&printTimer, "print-timer", false, "print systemd user units for hourly notifications")

	return cmd
}

func describe(r notify.Report) string {
	if r.Empty() {
		if r.Suppressed > 0 {
			return fmt.Sprintf("Nothing to send (%d already notified today).", r.Suppressed)
		}
		return "Nothing due."
	}

	out := ""
	for _, item := range r.Overdue {
		out += fmt.Sprintf("overdue  #%d  %s (%dd late)\n", item.Entry.ID, item.Entry.Title, item.Days)
	}
	for _, item := range r.Today {
		out += fmt.Sprintf("today    #%d  %s\n", item.Entry.ID, item.Entry.Title)
	}
	if r.Suppressed > 0 {
		out += fmt.Sprintf("(%d suppressed, already notified today)\n", r.Suppressed)
	}
	return out
}

// systemdUnits prints a user timer rather than installing one. Writing into
// ~/.config/systemd/user without being asked is the sort of thing a task
// manager should not do on its own.
func systemdUnits() string {
	return `# Save as ~/.config/systemd/user/taskgo-notify.service
[Unit]
Description=taskgo due and overdue notifications

[Service]
Type=oneshot
ExecStart=%h/go/bin/taskgo notify

# Save as ~/.config/systemd/user/taskgo-notify.timer
[Unit]
Description=Run taskgo notifications hourly

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target

# Then:
#   systemctl --user daemon-reload
#   systemctl --user enable --now taskgo-notify.timer
`
}
