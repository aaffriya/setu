package manager

import (
	"testing"

	"setu/internal/device"
)

type rangedWhiteDevice struct{}

func (rangedWhiteDevice) ID() string                 { return "white" }
func (rangedWhiteDevice) Name() string               { return "White" }
func (rangedWhiteDevice) Brand() string              { return "test" }
func (rangedWhiteDevice) Model() string              { return "ranged_white" }
func (rangedWhiteDevice) MAC() string                { return "00:11:22:33:44:55" }
func (rangedWhiteDevice) Capabilities() []string     { return []string{device.CapColorTemp} }
func (rangedWhiteDevice) State() device.State        { return device.State{} }
func (rangedWhiteDevice) SetColorTemp(int) error     { return nil }
func (rangedWhiteDevice) ColorTempRange() (int, int) { return 2700, 6500 }

func TestViewOfIncludesColorTempRange(t *testing.T) {
	view := ViewOf(rangedWhiteDevice{})
	if view.ColorTempMin != 2700 || view.ColorTempMax != 6500 {
		t.Fatalf("color temperature range = %d–%d, want 2700–6500", view.ColorTempMin, view.ColorTempMax)
	}
}
