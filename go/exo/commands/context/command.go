package context

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage git host configurations",
		Long:  "Manage context configurations for git hosts and organizations used by exo init",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initStyles()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractiveContextSelection()
		},
	}

	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newUpdateCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newShowCommand())

	return cmd
}

func runInteractiveContextSelection() error {
	names, err := ListContexts()
	if err != nil {
		return err
	}

	if len(names) == 0 {
		fmt.Println("No contexts configured.")
		fmt.Println()
		fmt.Println("Use 'exo context create <name> --host <url> --org <org>' to create one.")
		return nil
	}

	// Build options with name + description
	var options []huh.Option[string]
	for _, name := range names {
		ctx, err := LoadContext(name)
		if err != nil {
			continue
		}
		label := name
		if ctx.Description != "" {
			label = fmt.Sprintf("%s - %s", name, ctx.Description)
		}
		options = append(options, huh.NewOption(label, name))
	}

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a context").
				Options(options...).
				Value(&selected),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	// Print full details of selected context
	return printContextDetails(selected)
}

func printContextDetails(name string) error {
	ctx, err := LoadContext(name)
	if err != nil {
		return err
	}

	fmt.Printf("Context: %s\n", SuccessStyle.Render(ctx.Name))
	if ctx.Description != "" {
		fmt.Printf("  Description: %s\n", ctx.Description)
	}
	fmt.Printf("  Host: %s\n", ctx.Host)
	fmt.Printf("  Org:  %s\n", ctx.Org)
	if ctx.Provider != "" {
		fmt.Printf("  Provider: %s\n", ctx.Provider)
	}
	if ctx.Template != "" {
		fmt.Printf("  Template: %s\n", ctx.Template)
	}
	if ctx.Repo != "" {
		fmt.Printf("  Repo: %s\n", ctx.Repo)
	}
	return nil
}

func newCreateCommand() *cobra.Command {
	var host, org, provider, template, repo, description string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			exists, err := ContextExists(name)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("context %s already exists", ErrorStyle.Render(name))
			}

			if host == "" {
				return fmt.Errorf("--host is required")
			}
			if org == "" {
				return fmt.Errorf("--org is required")
			}

			ctx := &Context{
				Name:        name,
				Host:        host,
				Org:         org,
				Provider:    provider,
				Template:    template,
				Repo:        repo,
				Description: description,
			}

			if err := SaveContext(ctx); err != nil {
				return err
			}

			fmt.Printf("Context %s created\n", SuccessStyle.Render(name))
			if description != "" {
				fmt.Printf("  Description: %s\n", description)
			}
			fmt.Printf("  Host: %s\n", host)
			fmt.Printf("  Org:  %s\n", org)
			if provider != "" {
				fmt.Printf("  Provider: %s\n", provider)
			}
			if template != "" {
				fmt.Printf("  Template: %s\n", template)
			}
			if repo != "" {
				fmt.Printf("  Repo: %s\n", repo)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "Git host URL (required)")
	cmd.Flags().StringVar(&org, "org", "", "Organization name (required)")
	cmd.Flags().StringVar(&provider, "provider", "", "Git provider type")
	cmd.Flags().StringVar(&template, "template", "", "Default template repository")
	cmd.Flags().StringVar(&repo, "repo", "", "Default repository name")
	cmd.Flags().StringVar(&description, "description", "", "Description of this context")

	return cmd
}

func newUpdateCommand() *cobra.Command {
	var host, org, provider, template, repo, description string

	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an existing context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			ctx, err := LoadContext(name)
			if err != nil {
				return err
			}

			if host != "" {
				ctx.Host = host
			}
			if org != "" {
				ctx.Org = org
			}
			if provider != "" {
				ctx.Provider = provider
			}
			if template != "" {
				ctx.Template = template
			}
			if repo != "" {
				ctx.Repo = repo
			}
			if description != "" {
				ctx.Description = description
			}

			if err := SaveContext(ctx); err != nil {
				return err
			}

			fmt.Printf("Context %s updated successfully\n", SuccessStyle.Render(name))
			if ctx.Description != "" {
				fmt.Printf("  Description: %s\n", ctx.Description)
			}
			fmt.Printf("  Host: %s\n", ctx.Host)
			fmt.Printf("  Org:  %s\n", ctx.Org)
			if ctx.Provider != "" {
				fmt.Printf("  Provider: %s\n", ctx.Provider)
			}
			if ctx.Template != "" {
				fmt.Printf("  Template: %s\n", ctx.Template)
			}
			if ctx.Repo != "" {
				fmt.Printf("  Repo: %s\n", ctx.Repo)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "Git host URL")
	cmd.Flags().StringVar(&org, "org", "", "Organization name")
	cmd.Flags().StringVar(&provider, "provider", "", "Git provider type")
	cmd.Flags().StringVar(&template, "template", "", "Default template repository")
	cmd.Flags().StringVar(&repo, "repo", "", "Default repository name")
	cmd.Flags().StringVar(&description, "description", "", "Description of this context")

	return cmd
}

func newDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if err := DeleteContext(name); err != nil {
				return err
			}

			fmt.Printf("Context %s deleted successfully\n", SuccessStyle.Render(name))
			return nil
		},
	}
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show a context",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			} else {
				name = ResolveContextName()
				if name == "" {
					return fmt.Errorf("no context specified (provide name or use --context flag)")
				}
			}

			return printContextDetails(name)
		},
	}
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all context names",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected argument: %s", args[0])
			}

			names, err := ListContexts()
			if err != nil {
				return err
			}

			if len(names) == 0 {
				fmt.Println("No contexts configured.")
				fmt.Println()
				fmt.Println("Use 'exo context create <name> --host <url> --org <org>' to create one.")
				return nil
			}

			for _, name := range names {
				fmt.Println(name)
			}

			return nil
		},
	}
}
