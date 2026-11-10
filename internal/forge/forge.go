// Package forge abstracts the code host (GitLab today, GitHub next) behind its own CLI's auth.
package forge

import (
	"fmt"
	"time"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// MR is a merge request (GitLab) or pull request (GitHub).
type MR struct {
	IID          int       `json:"iid"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	State        string    `json:"state"` // "opened" when open, whatever the forge calls it
	Draft        bool      `json:"draft"`
	SHA          string    `json:"sha"`
	SourceBranch string    `json:"source_branch"`
	TargetBranch string    `json:"target_branch"`
	Labels       []string  `json:"labels"`
	Author       User      `json:"author"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Note struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	System bool   `json:"system"`
	Author User   `json:"author"`
}

type Forge interface {
	Name() string
	// User is the account the CLI is logged in as; only its notes count as reviews.
	User() (*User, error)
	OpenMRs(project string) ([]MR, error)
	MR(project string, iid int) (MR, error)
	Notes(project string, iid int) ([]Note, error)
	PostNote(project string, iid int, body string) error
	// Git access for the mirror.
	CloneURL(project string) string
	CredentialHelper() string
	HeadRef(iid int) string
	// ParseURL extracts project and iid from a web URL of an MR on this forge.
	ParseURL(url string) (project string, iid int, ok bool)
}

type Config struct {
	Type    string
	Host    string
	Command string
}

func New(c Config) (Forge, error) {
	switch c.Type {
	case "gitlab":
		return NewGitLab(c.Host, c.Command), nil
	}
	return nil, fmt.Errorf("unsupported forge %q (supported: gitlab)", c.Type)
}
