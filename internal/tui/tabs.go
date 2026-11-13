package tui

import (
	"errors"
	"fmt"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
)

// The list has three GitLab tabs (keys 1-3) and, with --github, three GitHub tabs (keys 4-6).
const (
	tabsPerForge = 3
	maxTabs      = 2 * tabsPerForge
)

var tabScopes = [tabsPerForge]provider.Scope{provider.ScopeAuthored, provider.ScopeAssigned, provider.ScopeReviewing}

// tabCount is the number of tabs shown: GitLab's, plus GitHub's if enabled.
func (m Model) tabCount() int {
	if m.github != nil {
		return maxTabs
	}
	return tabsPerForge
}

// tabProvider is the provider whose MRs a tab lists (and which performs their actions).
func (m Model) tabProvider(i int) provider.Provider {
	if i < tabsPerForge {
		return m.gitlab
	}
	return m.github
}

func tabScope(i int) provider.Scope {
	return tabScopes[i%tabsPerForge]
}

func scopeName(scope provider.Scope) string {
	switch scope {
	case provider.ScopeAssigned:
		return "Assigned"
	case provider.ScopeReviewing:
		return "Reviewing"
	default:
		return "Authored"
	}
}

// itemsName is what a forge calls its merge requests.
func itemsName(p provider.Provider) string {
	if p != nil && p.Name() == "GitHub" {
		return "PRs"
	}
	return "MRs"
}

// switchTab shows the cached MRs of another tab.
func (m *Model) switchTab(i int) {
	if i < 0 || i >= m.tabCount() {
		return
	}
	m.activeTab = i
	m.mrs = m.tabMRs[i]
	m.cursor = 0
	m.scroll = 0
}

type tabResult struct {
	mrs []provider.MergeRequest
	err error
}

// tabsFetchedMsg carries the result of refreshing every tab; results[i] is valid for i < count.
type tabsFetchedMsg struct {
	results [maxTabs]tabResult
	count   int
}

// fetchTabs refreshes all tabs in parallel.
func fetchTabs(m Model) tea.Cmd {
	count := m.tabCount()
	providers := make([]provider.Provider, count)
	for i := range providers {
		providers[i] = m.tabProvider(i)
	}

	return func() tea.Msg {
		msg := tabsFetchedMsg{count: count}
		var wg sync.WaitGroup
		for i, p := range providers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mrs, err := p.ListMRs(tabScope(i))
				msg.results[i] = tabResult{mrs: mrs, err: err}
			}()
		}
		wg.Wait()
		return msg
	}
}

// applyTabResults stores fetched MRs. Tabs that failed keep their previous data, so one
// forge being down doesn't block updates of the other.
func (m *Model) applyTabResults(msg tabsFetchedMsg) {
	wasInitialLoad := m.loading
	m.loading = false
	m.refreshing = false

	var errs []string
	for i := 0; i < msg.count && i < m.tabCount(); i++ {
		r := msg.results[i]
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("%s %s: %v", m.tabProvider(i).Name(), scopeName(tabScope(i)), r.err))
			continue
		}
		if tabScope(i) == provider.ScopeReviewing {
			r.mrs = sortReviewingMRs(r.mrs)
		}
		m.tabMRs[i] = r.mrs
	}

	m.mrs = m.tabMRs[m.activeTab]
	if m.cursor >= len(m.mrs) {
		m.cursor = 0
		m.scroll = 0
	}

	switch {
	case len(errs) == 0:
		m.err = nil
		m.refreshErr = nil
		m.cache.Set(m.mrs)
	case wasInitialLoad && len(errs) == msg.count:
		// Nothing loaded at all: show the error screen
		m.err = errors.New(errs[0])
	default:
		// Keep showing existing data; retry on the next interval
		m.refreshErr = errors.New(errs[0])
		if len(errs) < msg.count {
			m.cache.Set(m.mrs)
		}
	}
}
