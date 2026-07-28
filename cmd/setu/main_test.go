package main

import (
	"reflect"
	"strings"
	"testing"

	"setu/internal/config"
)

func TestProductionDeviceCatalogExcludesExampleDriver(t *testing.T) {
	factory := config.NewFactory()
	registerDeviceTypes(factory)

	for _, deviceType := range factory.Types() {
		if strings.EqualFold(deviceType.Brand, "example") {
			t.Fatalf("production device catalog includes the non-hardware Example driver: %+v", deviceType)
		}
	}
}

func TestProductionDeviceCatalogGroupsManualPicker(t *testing.T) {
	factory := config.NewFactory()
	registerDeviceTypes(factory)

	want := []config.DeviceType{
		{Category: "Fan", Brand: "Atomberg", Driver: "fan", Label: "Fan"},
		{Category: "Fan", Brand: "Atomberg", Driver: "fan_light", Label: "Fan With Light"},
		{Category: "Light", Brand: "WiZ", Driver: "color_bulb", Label: "Colour Bulb"},
		{Category: "Light", Brand: "WiZ", Driver: "tunable_white", Label: "Tunable White Bulb"},
		{Category: "TV", Brand: "Samsung", Driver: "tizen", Label: "Tizen TV"},
		{Category: "Wake-on-LAN", Brand: "WoL", Driver: "device", Label: "Wake-on-LAN Target"},
	}
	if got := factory.Types(); !reflect.DeepEqual(got, want) {
		t.Fatalf("production catalog = %+v; want %+v", got, want)
	}
}
