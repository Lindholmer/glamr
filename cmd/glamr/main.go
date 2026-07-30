package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
	"glamr/internal/tui"
)

func main() {
	// Parse flags
	host := flag.String("host", "", "GitLab host (e.g., gitlab.infr.zglbl.net)")
	var repos stringList
	flag.Var(&repos, "repo", "Repository to query (can be specified multiple times). If omitted, queries all accessible repos.")
	noTUI := flag.Bool("no-tui", false, "Disable TUI and print to stdout")
	useGraphQL := flag.Bool("use-graphql", true, "Use GraphQL API (default: true, use --use-graphql=false for REST)")
	flag.Parse()

	// Create GitLab providers
	var providers []provider.Provider

	if len(repos) == 0 {
		// No repos specified - create a single provider that will query all repos
		fmt.Fprintln(os.Stderr, "No --repo specified, discovering all accessible repositories...")

		var p provider.Provider
		var err error
		if *useGraphQL {
			p, err = provider.NewGitLabGraphQLProvider(*host, "")
		} else {
			p, err = provider.NewGitLabProvider(*host, "")
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		providers = append(providers, p)
	} else {
		// Specific repos requested - create a provider for each
		for _, repo := range repos {
			var p provider.Provider
			var err error
			if *useGraphQL {
				p, err = provider.NewGitLabGraphQLProvider(*host, repo)
			} else {
				p, err = provider.NewGitLabProvider(*host, repo)
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating provider for %s: %v\n", repo, err)
				os.Exit(1)
			}
			providers = append(providers, p)
		}
	}

	// If no-tui flag, run in simple mode
	if *noTUI {
		for _, p := range providers {
			runSimpleMode(p)
		}
		return
	}

	// Run TUI
	m := tui.NewModel(providers...)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// stringList is a custom flag type for multiple values
type stringList []string

func (s *stringList) String() string {
	return fmt.Sprint(*s)
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runSimpleMode(p provider.Provider) {
	fmt.Printf("Connected to %s\n\n", p.Name())

	// Get current user
	user, err := p.GetCurrentUser()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting user: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Authenticated as: %s\n\n", user)

	// Fetch authored MRs
	fmt.Println("Fetching your MRs...")
	mrs, err := p.ListMRs(provider.ScopeAuthored)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing MRs: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d MRs:\n\n", len(mrs))

	for _, mr := range mrs {
		// Pipeline status with emoji
		pipelineStatus := "⚫ no pipeline"
		if mr.Pipeline != nil {
			switch mr.Pipeline.Status {
			case provider.PipelineFailed:
				pipelineStatus = "❌ failed"
			case provider.PipelineRunning:
				pipelineStatus = "🔵 running"
			case provider.PipelinePending:
				pipelineStatus = "⏸️  pending"
			case provider.PipelineSuccess:
				pipelineStatus = "✅ passed"
			case provider.PipelineSkipped:
				pipelineStatus = "⏭️  skipped"
			default:
				pipelineStatus = fmt.Sprintf("⚠️  %s", mr.Pipeline.Status)
			}
		}

		conflict := ""
		if mr.HasConflicts {
			conflict = " ⚠️ CONFLICTS"
		}

		approvals := ""
		if mr.ApprovalCount > 0 {
			if mr.UserApproved {
				approvals = fmt.Sprintf(" 👍 You + %d others approved", mr.ApprovalCount-1)
			} else {
				approvals = fmt.Sprintf(" ✓ %d approved", mr.ApprovalCount)
			}
		}

		statusIcon := "🟢"
		if mr.Status == provider.StatusDraft {
			statusIcon = "📝"
		} else if mr.Status == provider.StatusMerged {
			statusIcon = "✅"
		} else if mr.Status == provider.StatusClosed {
			statusIcon = "❌"
		}

		fmt.Printf("%s !%d %s\n", statusIcon, mr.IID, mr.Title)
		fmt.Printf("   %s → %s | %s%s%s\n",
			mr.SourceBranch, mr.TargetBranch,
			pipelineStatus, conflict, approvals)
		fmt.Printf("   %s\n\n", mr.WebURL)
	}
}
