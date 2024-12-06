package waiters

import (
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/monitor"
)

// d describes single event's detail.
type d struct {
	name  string
	value any
}

// mWrite adds single event into non-empty monitoring.
func mWrite(m monitor.Writer, src, eType string, details ...d) {
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

	m.Write(&monitor.Event{
		Source:  src,
		Type:    eType,
		At:      time.Now(),
		Details: dd,
	})
}
