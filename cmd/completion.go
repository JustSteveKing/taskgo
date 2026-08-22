package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

// registerCompletions attaches dynamic shell completion to the commands and
// flags where it saves real typing.
//
// Cobra generates the `completion` subcommand itself; what it cannot know is
// what is actually in the store. Completing a task id alone would be nearly
// useless — the point of `taskgo done <tab>` is to see the titles.
func (a *app) registerCompletions(root *cobra.Command) {
	for _, c := range root.Commands() {
		switch c.Name() {
		case "done", "reopen", "show", "edit", "note", "delete":
			c.ValidArgsFunction = a.completeTaskRef
		}

		// Flag completions are registered per command because each command
		// declares its own flag set.
		if c.Flags().Lookup("status") != nil {
			_ = c.RegisterFlagCompletionFunc("status", completeStatus)
		}
		if c.Flags().Lookup("priority") != nil {
			_ = c.RegisterFlagCompletionFunc("priority", completePriority)
		}
		if c.Flags().Lookup("project") != nil {
			_ = c.RegisterFlagCompletionFunc("project", a.completeProject)
		}
		if c.Flags().Lookup("tag") != nil {
			_ = c.RegisterFlagCompletionFunc("tag", a.completeTag)
		}
		if c.Flags().Lookup("due") != nil {
			_ = c.RegisterFlagCompletionFunc("due", completeDue)
		}
	}
}

// completeTaskRef offers "id<TAB>title" pairs. Cobra splits a completion on
// the first tab and shows the right-hand side as the description, so the shell
// inserts the id while the user reads the title.
func (a *app) completeTaskRef(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	s, err := a.openStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// Completing `done` should not offer things that are already done, but
	// `reopen` should offer nothing else.
	filter := store.Filter{}
	if cmd.Name() == "reopen" {
		filter.Status = store.StatusDone
	}

	entries, err := s.List(filter)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		id := strconv.Itoa(e.ID)
		if toComplete != "" && !strings.HasPrefix(id, toComplete) {
			continue
		}
		out = append(out, id+"\t"+e.Title)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completeProject(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	s, err := a.openStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	projects, err := s.ListProjects()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var out []string
	for _, p := range projects {
		if toComplete != "" && !strings.HasPrefix(p.Name, toComplete) {
			continue
		}
		out = append(out, fmt.Sprintf("%s\t%d open", p.Name, p.Open))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeTag reads tags off the index rather than keeping a separate list, so
// a tag stops being offered as soon as nothing carries it.
func (a *app) completeTag(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	s, err := a.openStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	entries, err := s.List(store.Filter{IncludeDone: true})
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	seen := map[string]int{}
	for _, e := range entries {
		for _, tag := range e.Tags {
			seen[tag]++
		}
	}

	var out []string
	for tag, n := range seen {
		if toComplete != "" && !strings.HasPrefix(tag, toComplete) {
			continue
		}
		label := "tasks"
		if n == 1 {
			label = "task"
		}
		out = append(out, fmt.Sprintf("%s\t%d %s", tag, n, label))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeStatus(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return filterPrefix([]string{
		"todo\tnot started",
		"doing\tin progress",
		"blocked\twaiting on something",
		"done\tcomplete",
	}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completePriority(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return filterPrefix([]string{
		"urgent\tdrop everything",
		"high\tsoon",
		"normal\tdefault",
		"low\twhenever",
	}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeDue(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return filterPrefix([]string{
		"today\t",
		"tomorrow\t",
	}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func filterPrefix(candidates []string, toComplete string) []string {
	if toComplete == "" {
		return candidates
	}
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, toComplete) {
			out = append(out, c)
		}
	}
	return out
}
