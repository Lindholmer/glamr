package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
	"glamr/internal/readlist"
	"glamr/internal/tui"
)

func main() {
	// Parse flags
	withGitLab := flag.Bool("gitlab", false, "Show GitLab merge requests, using the glab CLI")
	host := flag.String("host", "", "GitLab host (e.g. gitlab.example.com). Defaults to glab's configured host. Implies --gitlab.")
	var repos stringList
	flag.Var(&repos, "repo", "GitLab repository to query (can be specified multiple times). If omitted, all accessible repos. Implies --gitlab.")
	noTUI := flag.Bool("no-tui", false, "Disable TUI and print to stdout")
	withGitHub := flag.Bool("github", false, "Show GitHub pull requests, using the gh CLI")
	githubHost := flag.String("github-host", "", "GitHub Enterprise host. Defaults to gh's configured host (github.com). Implies --github.")
	var githubRepos stringList
	flag.Var(&githubRepos, "github-repo", "GitHub repository to include (owner/name, can be repeated). If omitted, all repos. Implies --github.")
	mentionDays := flag.Int("mention-days", 7, "How many days back the Mentions tab looks")
	flag.Parse()

	if *mentionDays < 1 {
		fmt.Fprintln(os.Stderr, "Error: --mention-days must be at least 1")
		os.Exit(1)
	}

	useGitLab := forgeEnabled(*withGitLab, *host, repos)
	useGitHub := forgeEnabled(*withGitHub, *githubHost, githubRepos)
	if !useGitLab && !useGitHub {
		fmt.Fprintln(os.Stderr, "Nothing to show: pass --gitlab and/or --github (or a flag that implies them, e.g. --host or --github-repo).")
		fmt.Fprintln(os.Stderr, "Run glamr --help for all flags.")
		os.Exit(2)
	}

	// Typed as the interface so a disabled forge is a real nil, not a nil pointer in an interface
	var gitlab, github provider.Provider
	if useGitLab {
		// --repo filters the user's MRs client-side, so a single provider serves any number of repos
		p, err := provider.NewGitLabGraphQLProvider(*host, repos...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		gitlab = p
	}
	if useGitHub {
		p, err := provider.NewGitHubProvider(*githubHost, githubRepos...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		github = p
	}

	// If no-tui flag, run in simple mode
	if *noTUI {
		for i, p := range []provider.Provider{gitlab, github} {
			if p == nil {
				continue
			}
			if i > 0 && gitlab != nil {
				fmt.Println()
			}
			runSimpleMode(p)
		}
		return
	}

	// Mentions marked read are kept in a small state file
	readPath, err := readlist.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	reads, err := readlist.Load(readPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v (delete the file to start over)\n", err)
		os.Exit(1)
	}

	// Run TUI
	m := tui.NewModel(gitlab, github, reads, time.Duration(*mentionDays)*24*time.Hour)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// forgeEnabled reports whether a forge should be queried: its flag is set, or one of its
// host/repo flags is (they imply it).
func forgeEnabled(flag bool, host string, repos []string) bool {
	return flag || host != "" || len(repos) > 0
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
	items := "MRs"
	if p.Name() == "GitHub" {
		items = "PRs"
	}
	fmt.Printf("Fetching your %s...\n", items)
	mrs, err := p.ListMRs(provider.ScopeAuthored)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing %s: %v\n", items, err)
		os.Exit(1)
	}

	fmt.Printf("Found %d %s:\n\n", len(mrs), items)

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

		fmt.Printf("%s %s %s\n", statusIcon, mr.Ref(), mr.Title)
		fmt.Printf("   %s → %s | %s%s%s\n",
			mr.SourceBranch, mr.TargetBranch,
			pipelineStatus, conflict, approvals)
		fmt.Printf("   %s\n\n", mr.WebURL)
	}
}
