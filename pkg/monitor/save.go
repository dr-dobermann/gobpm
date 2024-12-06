package monitor

import (
	"strings"
	"time"
)

// d describes single event's detail.
type d struct {
	name  string
	value any
}

// D returns the new details object created from name and value.
func D(name string, value any) d {
	return d{
		name:  name,
		value: value,
	}
}

// mWrite adds single event into non-empty monitoring.
func Save(m Writer, src, eType string, details ...d) {
	if m == nil {
		return
	}

	dd := map[string]any{}
	for _, d := range details {
		d.name = strings.TrimSpace(d.name)
		if d.name == "" || d.value == nil {
			continue
		}

		dd[d.name] = d.value
	}

	m.Write(&Event{
		Source:  src,
		Type:    eType,
		At:      time.Now(),
		Details: dd,
	})
}
