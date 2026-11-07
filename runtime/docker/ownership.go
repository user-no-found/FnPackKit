package docker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type Container struct {
	Name, Project, Service string
	Labels                 map[string]string
}

type Volume struct {
	Name, Project, Key string
	Labels             map[string]string
}

func owned(actual, extra map[string]string, project, kind, value string) bool {
	if actual["com.docker.compose.project"] != project || actual["com.docker.compose."+kind] != value {
		return false
	}
	for key, expected := range extra {
		if actual[key] != expected {
			return false
		}
	}
	return true
}

func (c Client) Running(ctx context.Context, expected Container) (bool, error) {
	if !namePattern.MatchString(expected.Name) || !projectPattern.MatchString(expected.Project) || !namePattern.MatchString(expected.Service) {
		return false, errors.New("invalid Docker container identity")
	}
	data, err := c.run(ctx, "container", "inspect", "--format", `{"running":{{json .State.Running}},"labels":{{json .Config.Labels}}}`, expected.Name)
	if err != nil {
		if _, check := c.run(ctx, "info", "--format", "{{.ID}}"); check != nil {
			return false, check
		}
		return false, nil
	}
	var state struct {
		Running bool              `json:"running"`
		Labels  map[string]string `json:"labels"`
	}
	if json.Unmarshal(data, &state) != nil || !owned(state.Labels, expected.Labels, expected.Project, "service", expected.Service) {
		return false, errors.New("Docker container does not belong to this application")
	}
	return state.Running, nil
}

func (c Client) inspectVolume(ctx context.Context, expected Volume) (bool, error) {
	if !namePattern.MatchString(expected.Name) || !projectPattern.MatchString(expected.Project) || !namePattern.MatchString(expected.Key) {
		return false, errors.New("invalid Docker volume identity")
	}
	data, err := c.run(ctx, "volume", "inspect", "--format", "{{json .Labels}}", expected.Name)
	if err != nil {
		all, check := c.run(ctx, "volume", "ls", "--format", "{{.Name}}")
		if check != nil {
			return false, check
		}
		for _, name := range strings.Fields(string(all)) {
			if name == expected.Name {
				return false, err
			}
		}
		return false, nil
	}
	var labels map[string]string
	if json.Unmarshal(data, &labels) != nil || !owned(labels, expected.Labels, expected.Project, "volume", expected.Key) {
		return false, errors.New("Docker volume does not belong to this application")
	}
	return true, nil
}

// CheckVolume rejects foreign existing data and permits an absent new volume.
func (c Client) CheckVolume(ctx context.Context, expected Volume) error {
	_, err := c.inspectVolume(ctx, expected)
	return err
}

// RemoveVolume deletes only an existing volume with all required ownership labels.
func (c Client) RemoveVolume(ctx context.Context, expected Volume) error {
	exists, err := c.inspectVolume(ctx, expected)
	if err != nil || !exists {
		return err
	}
	_, err = c.run(ctx, "volume", "rm", expected.Name)
	return err
}
