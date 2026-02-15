package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type ActivityItem struct {
	ID        string
	Icon      string
	Message   string
	StartedAt time.Time
	DoneAt    *time.Time
	Cost      float64
}

type ActivityFeed struct {
	mu        sync.Mutex
	items     []ActivityItem
	collapsed bool
	maxItems  int
}

func NewActivityFeed() *ActivityFeed {
	return &ActivityFeed{maxItems: 10, collapsed: true}
}

func (f *ActivityFeed) Add(item ActivityItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
	if len(f.items) > f.maxItems {
		f.items = f.items[1:]
	}
	f.collapsed = false // auto-expand
}

func (f *ActivityFeed) Complete(id, icon string, cost float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].Icon = icon
			f.items[i].DoneAt = &now
			f.items[i].Cost = cost
			return
		}
	}
}

func (f *ActivityFeed) Toggle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.collapsed = !f.collapsed
}

func (f *ActivityFeed) HasActive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.items {
		if it.DoneAt == nil {
			return true
		}
	}
	return false
}

func (f *ActivityFeed) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

func (f *ActivityFeed) CleanupOld(maxAge time.Duration) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	kept := f.items[:0]
	removed := 0
	for _, it := range f.items {
		if it.DoneAt != nil && now.Sub(*it.DoneAt) >= maxAge {
			removed++
			continue
		}
		kept = append(kept, it)
	}
	f.items = kept
	return removed
}

func (f *ActivityFeed) View() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.items) == 0 {
		return ""
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	if f.collapsed {
		active := 0
		for _, it := range f.items {
			if it.DoneAt == nil {
				active++
			}
		}
		if active == 0 {
			return ""
		}
		return dim.Render(fmt.Sprintf("── %d active tasks (Ctrl+A to expand) ──", active)) + "\n"
	}

	itemS := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	costS := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	var out strings.Builder
	out.WriteString(dim.Render("── Activity (Ctrl+A to collapse) ──") + "\n")
	for _, it := range f.items {
		line := fmt.Sprintf("%s %s", it.Icon, it.Message)
		if it.DoneAt != nil {
			dur := it.DoneAt.Sub(it.StartedAt).Truncate(100 * time.Millisecond)
			line += fmt.Sprintf(" (%s)", dur)
			if it.Cost > 0 {
				line += costS.Render(fmt.Sprintf(" $%.4f", it.Cost))
			}
		} else {
			line += fmt.Sprintf(" (%s)", time.Since(it.StartedAt).Truncate(time.Second))
		}
		out.WriteString(itemS.Render(line) + "\n")
	}
	return out.String()
}
