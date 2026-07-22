package compose

import (
	"bytes"
	"encoding/json"
	"sort"
)

type Model struct {
	Name     string             `json:"name"`
	Services map[string]Service `json:"services"`
}

type Service struct {
	Image string          `json:"image"`
	Build json.RawMessage `json:"build"`
}

func (s Service) HasBuild() bool {
	value := bytes.TrimSpace(s.Build)
	return len(value) > 0 && !bytes.Equal(value, []byte("null"))
}

func (m Model) SortedServiceNames() []string {
	names := make([]string, 0, len(m.Services))
	for name := range m.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
