package ui_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mallardduck/BrambleGate/internal/gui/ui"
)

func TestToastLevels(t *testing.T) {
	tests := []struct {
		name  string
		level ui.ToastLevel
		want  []string
	}{
		{"error", ui.ToastError, []string{`data-toast-level="error"`, "border-bramble-danger", "text-bramble-danger"}},
		{"success", ui.ToastSuccess, []string{`data-toast-level="success"`, "border-bramble-success", "text-bramble-success"}},
		{"info", ui.ToastInfo, []string{`data-toast-level="info"`, "border-bramble-primary", "text-bramble-primary"}},
		{"warning", ui.ToastWarning, []string{`data-toast-level="warning"`, "border-bramble-warning", "text-bramble-warning"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ui.Toast(tc.level, "hello <b>world</b>").Render(t.Context(), &buf); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			// Every level shares the oob swap target, the data-toast marker toast.js
			// keys auto-dismiss on, and the close button.
			for _, want := range append([]string{
				`hx-swap-oob="afterbegin:#toast-container"`, "data-toast", "data-toast-close",
				"hello &lt;b&gt;world&lt;/b&gt;", // message is escaped
			}, tc.want...) {
				if !strings.Contains(out, want) {
					t.Errorf("toast missing %q:\n%s", want, out)
				}
			}
		})
	}
}

// The icon is optional: it defaults from the level, and WithToastIcon overrides
// it without touching the level's colours.
func TestToastIcon(t *testing.T) {
	tests := []struct {
		name     string
		level    ui.ToastLevel
		opts     []ui.ToastOption
		wantIcon string
	}{
		{"error default", ui.ToastError, nil, "alert-triangle"},
		{"success default", ui.ToastSuccess, nil, "check"},
		{"info default", ui.ToastInfo, nil, "info"},
		{"warning default", ui.ToastWarning, nil, "alert-triangle"},
		{"override on success", ui.ToastSuccess, []ui.ToastOption{ui.WithToastIcon(ui.ToastIconAlertTriangle)}, "alert-triangle"},
		{"override on error", ui.ToastError, []ui.ToastOption{ui.WithToastIcon(ui.ToastIconInfo)}, "info"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ui.Toast(tc.level, "msg", tc.opts...).Render(t.Context(), &buf); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			if want := `data-toast-icon="` + tc.wantIcon + `"`; !strings.Contains(out, want) {
				t.Errorf("toast missing %s:\n%s", want, out)
			}
			if n := strings.Count(out, "data-toast-icon="); n != 1 {
				t.Errorf("expected exactly one icon, got %d:\n%s", n, out)
			}
		})
	}
}

func TestToastIconOverrideKeepsLevelColours(t *testing.T) {
	var buf bytes.Buffer
	toast := ui.Toast(ui.ToastSuccess, "msg", ui.WithToastIcon(ui.ToastIconAlertTriangle))
	if err := toast.Render(t.Context(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "border-bramble-success") || strings.Contains(out, "bramble-danger") {
		t.Errorf("an icon override must not change the level's colours:\n%s", out)
	}
}

// Colour follows the same shape as the icon: defaulted from the level,
// overridable via WithToastColor, and independent of the icon.
func TestToastColorOverride(t *testing.T) {
	tests := []struct {
		name      string
		level     ui.ToastLevel
		opts      []ui.ToastOption
		wantClass []string
		notClass  []string
		wantIcon  string
	}{
		{
			name: "info recoloured as danger keeps the info icon", level: ui.ToastInfo,
			opts:      []ui.ToastOption{ui.WithToastColor(ui.ToastColorDanger)},
			wantClass: []string{"border-bramble-danger", "text-bramble-danger"},
			notClass:  []string{"bramble-primary", "bramble-success"},
			wantIcon:  "info",
		},
		{
			name: "error recoloured as success keeps the alert icon", level: ui.ToastError,
			opts:      []ui.ToastOption{ui.WithToastColor(ui.ToastColorSuccess)},
			wantClass: []string{"border-bramble-success", "text-bramble-success"},
			notClass:  []string{"bramble-danger"},
			wantIcon:  "alert-triangle",
		},
		{
			name: "info recoloured as warning keeps the info icon", level: ui.ToastInfo,
			opts:      []ui.ToastOption{ui.WithToastColor(ui.ToastColorWarning)},
			wantClass: []string{"border-bramble-warning", "text-bramble-warning"},
			notClass:  []string{"bramble-primary", "bramble-danger"},
			wantIcon:  "info",
		},
		{
			name: "colour and icon override together", level: ui.ToastSuccess,
			opts:      []ui.ToastOption{ui.WithToastColor(ui.ToastColorPrimary), ui.WithToastIcon(ui.ToastIconInfo)},
			wantClass: []string{"border-bramble-primary", "text-bramble-primary"},
			notClass:  []string{"bramble-danger", "bramble-success"},
			wantIcon:  "info",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ui.Toast(tc.level, "msg", tc.opts...).Render(t.Context(), &buf); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, want := range tc.wantClass {
				if !strings.Contains(out, want) {
					t.Errorf("toast missing %q:\n%s", want, out)
				}
			}
			for _, not := range tc.notClass {
				if strings.Contains(out, not) {
					t.Errorf("toast must not contain %q:\n%s", not, out)
				}
			}
			if want := `data-toast-icon="` + tc.wantIcon + `"`; !strings.Contains(out, want) {
				t.Errorf("toast missing %s:\n%s", want, out)
			}
			// The level is semantic metadata and is unaffected by presentation overrides.
			if !strings.Contains(out, `data-toast-level="`) {
				t.Errorf("toast missing its level marker:\n%s", out)
			}
		})
	}
}

func TestToastLevelsDoNotLeakStyles(t *testing.T) {
	var buf bytes.Buffer
	if err := ui.Toast(ui.ToastSuccess, "ok").Render(t.Context(), &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "bramble-danger") {
		t.Errorf("a success toast must not carry error styling:\n%s", buf.String())
	}
}
