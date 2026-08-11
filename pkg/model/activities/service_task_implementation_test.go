package activities_test

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// TestWithImplementation covers the BPMN `implementation` carrier. BPMN
// puts the attribute on the ServiceTask itself, while gobpm otherwise
// derives it from the Operation's Implementor — which an imported
// operation deliberately lacks, leaving a document's own hint nowhere to
// live.
func TestWithImplementation(t *testing.T) {
	op, err := service.NewOperation("charge", nil, nil, nil)
	if err != nil {
		t.Fatalf("operation: %v", err)
	}

	t.Run("an explicit hint wins over the derived value", func(t *testing.T) {
		st, err := activities.NewServiceTask("Charge", op,
			activities.WithoutParams(),
			activities.WithImplementation("##WebService"))
		if err != nil {
			t.Fatalf("NewServiceTask: %v", err)
		}

		if got := st.Implementation(); got != "##WebService" {
			t.Errorf("Implementation() = %q, want the explicit hint", got)
		}
	})

	t.Run("the hint is trimmed", func(t *testing.T) {
		st, err := activities.NewServiceTask("Charge", op,
			activities.WithoutParams(),
			activities.WithImplementation("  ##WebService  "))
		if err != nil {
			t.Fatalf("NewServiceTask: %v", err)
		}

		if got := st.Implementation(); got != "##WebService" {
			t.Errorf("Implementation() = %q, want it trimmed", got)
		}
	})

	t.Run("unset, the operation's type still stands", func(t *testing.T) {
		st, err := activities.NewServiceTask("Charge", op,
			activities.WithoutParams())
		if err != nil {
			t.Fatalf("NewServiceTask: %v", err)
		}

		if got := st.Implementation(); got != service.UnspecifiedImplementation {
			t.Errorf("Implementation() = %q, want the derived value — "+
				"no existing caller may change behaviour", got)
		}
	})

	t.Run("a blank hint is rejected", func(t *testing.T) {
		// Accepting it would overwrite the derived value with nothing,
		// which is the class of defect the public-API validation rule
		// exists to prevent.
		for _, blank := range []string{"", "   "} {
			_, err := activities.NewServiceTask("Charge", op,
				activities.WithoutParams(),
				activities.WithImplementation(blank))
			if err == nil {
				t.Errorf("WithImplementation(%q): want an error", blank)

				continue
			}

			if !strings.Contains(err.Error(), "WithImplementation") {
				t.Errorf("error %q does not name the option", err)
			}
		}
	})
}
