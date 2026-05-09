package gcp

import (
	"context"
	"fmt"
	"time"
)

// Project is a subset of the Cloud Resource Manager projects.Project
// resource — just the fields charon's TUI and storage care about.
// See https://cloud.google.com/resource-manager/reference/rest/v1/projects.
type Project struct {
	ProjectID      string  `json:"projectId"`
	Name           string  `json:"name"` // display name
	LifecycleState string  `json:"lifecycleState"`
	Parent         *Parent `json:"parent,omitempty"`
}

// Parent identifies a project's containing organization or folder.
// nil means no parent (a "personal" project, the MVP default).
type Parent struct {
	Type string `json:"type"` // "organization" or "folder"
	ID   string `json:"id"`
}

// projectsListResponse models the v1 projects.list payload.
type projectsListResponse struct {
	Projects      []Project `json:"projects"`
	NextPageToken string    `json:"nextPageToken,omitempty"`
}

// ListProjects returns every ACTIVE project the caller has access
// to. Pages are walked transparently. Non-ACTIVE projects (DELETE_
// REQUESTED, DELETE_IN_PROGRESS) are filtered out — they're not
// usable as Vertex/AI-Studio destinations and would just confuse
// the picker.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var all []Project
	pageToken := ""
	for {
		url := c.ResourceManager + "/v1/projects?pageSize=200"
		if pageToken != "" {
			url += "&pageToken=" + pageToken
		}
		var page projectsListResponse
		if err := c.do(ctx, "GET", url, nil, &page); err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		for _, p := range page.Projects {
			if p.LifecycleState == "ACTIVE" {
				all = append(all, p)
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return all, nil
}

// createProjectRequest models the v1 projects.create payload.
type createProjectRequest struct {
	ProjectID string  `json:"projectId"`
	Name      string  `json:"name"`
	Parent    *Parent `json:"parent,omitempty"`
}

// Operation models the long-running operation response shape used by
// resourcemanager.projects.create and many other GCP APIs. Done is
// false until the project is fully provisioned; Error is populated
// only when the operation failed.
type Operation struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Error    *OperationError `json:"error,omitempty"`
	Response map[string]any  `json:"response,omitempty"`
}

// OperationError mirrors google.rpc.Status — the standard envelope
// for failed long-running operations.
type OperationError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("gcp operation: code %d: %s", e.Code, e.Message)
}

// CreateProject kicks off project creation and returns the
// long-running operation name. The project is not usable until the
// operation completes — call WaitOperation to block.
//
// projectID must be globally unique across GCP and follow the lower-
// case alnum + hyphen rules (6-30 chars). Display name is freeform
// up to 30 chars. Parent is nil for personal projects (MVP).
func (c *Client) CreateProject(ctx context.Context, projectID, displayName string, parent *Parent) (*Operation, error) {
	req := &createProjectRequest{
		ProjectID: projectID,
		Name:      displayName,
		Parent:    parent,
	}
	var op Operation
	if err := c.do(ctx, "POST", c.ResourceManager+"/v1/projects", req, &op); err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	return &op, nil
}

// WaitOperation polls the named long-running operation until done or
// the context's deadline / timeout fires. pollInterval governs how
// often the operation is queried; 2s is a sensible default for
// Resource Manager's typical 5-30s project-create latency.
//
// Returns nil on successful completion, the operation's Error on
// failure, or a context error on timeout.
func (c *Client) WaitOperation(ctx context.Context, opName string, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	url := c.ResourceManager + "/v1/" + opName
	for {
		var op Operation
		if err := c.do(ctx, "GET", url, nil, &op); err != nil {
			return fmt.Errorf("poll operation: %w", err)
		}
		if op.Done {
			if op.Error != nil {
				return op.Error
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
