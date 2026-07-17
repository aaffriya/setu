package wiz

import "setu/internal/device"

// WiZ white color-temperature range (Kelvin) and scene animation speed range.
// Values outside are clamped.
const (
	minKelvin             = 2200
	tunableWhiteMinKelvin = 2700
	maxKelvin             = 6500
	minSpeed              = 10
	maxSpeed              = 200
)

// sceneNames is WiZ's fixed catalogue of predefined scenes; the scene id is the
// 1-based index (id 1 = "Ocean" … id 32 = "Steampunk"). This is the well-known
// WiZ list and matches what the bulb reports via getPilot (e.g. id 11 = "Warm
// White", id 14 = "Night light").
var sceneNames = []string{
	"Ocean", "Romance", "Sunset", "Party", "Fireplace", "Cozy", "Forest",
	"Pastel Colors", "Wake up", "Bedtime", "Warm White", "Daylight", "Cool white",
	"Night light", "Focus", "Relax", "True colors", "TV time", "Plantgrowth",
	"Spring", "Summer", "Fall", "Deepdive", "Jungle", "Mojito", "Club",
	"Christmas", "Halloween", "Candlelight", "Golden white", "Pulse", "Steampunk",
}

// dynamicSceneIDs are the animated scenes whose speed can be adjusted (the WiZ
// app's "Dynamic" group). The rest are static (White / Functional / Progressive
// in the app) and ignore the speed parameter, so the UI hides the speed slider
// for them.
var dynamicSceneIDs = map[int]bool{
	1: true, 2: true, 3: true, 4: true, 5: true, 7: true, 8: true,
	20: true, 21: true, 22: true, 23: true, 24: true, 25: true, 26: true,
	27: true, 28: true, 29: true, 31: true, 32: true,
}

// scenes is the catalogue as device.Scene values, built once. Treated as
// read-only (it's only serialized to JSON for the UI).
var scenes = buildScenes()

// Tunable-white WiZ lights ignore the colour-only modes. This conservative
// subset is the white/functional group supported by ESP*_SHTW modules. The
// device used to add this model was read back successfully in every mode 9–16,
// while mode 1 (Ocean) was ignored.
var tunableWhiteScenes = scenes[8:16]

func buildScenes() []device.Scene {
	out := make([]device.Scene, len(sceneNames))
	for i, name := range sceneNames {
		id := i + 1
		out[i] = device.Scene{ID: id, Name: name, Dynamic: dynamicSceneIDs[id]}
	}
	return out
}
