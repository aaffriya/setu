package automation

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"setu/internal/control"
	"setu/internal/device"
	"setu/internal/manager"
	"setu/internal/resolver"
)

const (
	FormatVersion    = 1
	MaxRules         = 64
	MaxConditions    = 4
	MaxActions       = 16
	MaxDelay         = 60
	MaxOffsetMinutes = 1439
	MaxNesting       = 8
	MaxRunActions    = MaxActions * MaxNesting
	MaxRunDelay      = MaxActions * MaxDelay
)

const (
	TriggerSchedule      = "schedule"
	TriggerDeviceState   = "device_state"
	TriggerDeviceOffline = "device_offline"
	TriggerDeviceOnline  = "device_online"
	TriggerPresence      = "presence"
	TriggerWebhook       = "webhook"
	ActionAutomation     = "run_automation"
)

// Metrics a device-state trigger can watch. MetricPower is the on/off edge and
// is what an omitted metric means, so rules written before the others existed
// keep their meaning.
const (
	MetricPower      = "power"
	MetricBrightness = "brightness"
	MetricSpeed      = "speed"
	MetricVolume     = "volume"
	MetricColorTemp  = "color_temp"
	MetricTimerHours = "timer_hours"
)

// Comparisons available to a numeric metric.
const (
	OpAbove  = "above"
	OpBelow  = "below"
	OpEquals = "equals"
)

const (
	// MaxStableSeconds bounds the settle time on a device-state trigger.
	MaxStableSeconds = 300
	// MaxPresenceStableSeconds is larger because it is doing a different job: a
	// phone's ARP entry disappears and returns as it sleeps, so "away" usually
	// needs several minutes of quiet before it means anything.
	MaxPresenceStableSeconds = 900
	// MaxOfflineMinutes bounds a "has been unreachable for" trigger at a day.
	MaxOfflineMinutes = 1440
	// MaxMetricValue covers every metric's range, the largest being colour
	// temperature in Kelvin.
	MaxMetricValue = 10000
)

// metricCapability maps a watchable metric to the capability a device must
// report for the value to mean anything.
var metricCapability = map[string]string{
	MetricBrightness: device.CapBrightness,
	MetricSpeed:      device.CapSpeed,
	MetricVolume:     device.CapVolume,
	MetricColorTemp:  device.CapColorTemp,
	MetricTimerHours: device.CapTimer,
}

// metricValue reads one watchable number out of a device state.
func metricValue(metric string, state device.State) (int, bool) {
	switch metric {
	case MetricBrightness:
		return state.Brightness, true
	case MetricSpeed:
		return state.Speed, true
	case MetricVolume:
		return state.Volume, true
	case MetricColorTemp:
		return state.ColorTemp, true
	case MetricTimerHours:
		return state.TimerHours, true
	}
	return 0, false
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// State is the complete persistent automation configuration. Runtime history,
// queues, cooldown clocks, and webhook rate limits deliberately live in RAM.
type State struct {
	Version  int    `json:"version"`
	Revision uint64 `json:"revision"`
	Paused   bool   `json:"paused"`
	Items    []Rule `json:"items"`
}

type Rule struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Enabled         bool        `json:"enabled"`
	Trigger         Trigger     `json:"trigger"`
	Conditions      []Condition `json:"conditions,omitempty"`
	Actions         []Action    `json:"actions"`
	CooldownSeconds int         `json:"cooldown_seconds,omitempty"`
}

type Trigger struct {
	Type     string          `json:"type"`
	Schedule *Schedule       `json:"schedule,omitempty"`
	Device   *DeviceTrigger  `json:"device,omitempty"`
	Offline  *OfflineTrigger `json:"offline,omitempty"`
	Online   *OnlineTrigger  `json:"online,omitempty"`
	Presence *Presence       `json:"presence,omitempty"`
	Webhook  *Webhook        `json:"webhook,omitempty"`
}

type Schedule struct {
	Time             string `json:"time"`     // HH:MM
	Weekdays         []int  `json:"weekdays"` // 0=Sunday ... 6=Saturday
	UTCOffsetMinutes int    `json:"utc_offset_minutes"`
}

// DeviceTrigger fires on the edge where a device's state starts matching it.
//
// With no Metric — or MetricPower — that edge is the on/off transition this
// trigger has always meant. A numeric metric instead compares one reported value
// against Value, and fires the moment the comparison starts holding: crossing 50%
// brightness is an event, sitting above it is not.
type DeviceTrigger struct {
	DeviceID string `json:"device_id"`
	On       bool   `json:"on"`
	// StableSeconds holds the trigger back until the device has matched
	// continuously for that long.
	StableSeconds int `json:"stable_seconds,omitempty"`
	// Metric names the value to watch; empty means power.
	Metric string `json:"metric,omitempty"`
	// Operator and Value apply to numeric metrics only.
	Operator string `json:"operator,omitempty"`
	Value    int    `json:"value,omitempty"`
}

// power reports whether this trigger watches the on/off edge.
func (t DeviceTrigger) power() bool { return t.Metric == "" || t.Metric == MetricPower }

// metric returns the metric this trigger watches, normalising the empty default.
func (t DeviceTrigger) metric() string {
	if t.power() {
		return MetricPower
	}
	return t.Metric
}

// matches reports whether a state satisfies this trigger right now.
//
// A numeric metric additionally requires the device to be reachable: an
// unreachable device reports zeros, and a bulb that has merely lost Wi-Fi must
// not read as "brightness fell below 20". Power keeps its original behaviour,
// where losing contact is itself meaningful.
func (t DeviceTrigger) matches(state device.State) bool {
	if t.power() {
		return state.On == t.On
	}
	if !state.Online {
		return false
	}
	value, ok := metricValue(t.Metric, state)
	if !ok {
		return false
	}
	switch t.Operator {
	case OpAbove:
		return value > t.Value
	case OpBelow:
		return value < t.Value
	case OpEquals:
		return value == t.Value
	}
	return false
}

// OfflineTrigger fires once when a device has been unreachable for Minutes,
// and arms again only after it has been seen online. It is the "did the NAS
// fall off the network?" rule, and it is deliberately not an edge on the online
// flag: a device that flaps every few seconds must not produce a run each time.
type OfflineTrigger struct {
	DeviceID string `json:"device_id"`
	Minutes  int    `json:"minutes"`
}

// OnlineTrigger fires when a device returns after one observed unreachable
// episode whose completed minutes match Operator and Minutes. The episode clock
// is RAM-only, so a restart starts measuring an already-offline device again.
type OnlineTrigger struct {
	DeviceID string `json:"device_id"`
	Operator string `json:"operator"`
	Minutes  int    `json:"minutes"`
}

func (t OnlineTrigger) matches(offlineFor time.Duration) bool {
	minutes := int(offlineFor / time.Minute)
	switch t.Operator {
	case OpAbove:
		return minutes > t.Minutes
	case OpBelow:
		return minutes < t.Minutes
	case OpEquals:
		return minutes == t.Minutes
	}
	return false
}

// Presence fires when a MAC appears on — or disappears from — the LAN, as seen
// in the kernel's neighbour table. It is how "when my phone gets home" is
// written without any device being added for the phone.
//
// It is best-effort by nature. A neighbour entry can linger after a device
// leaves and can vanish while a phone merely sleeps, which is what StableSeconds
// is for: require the new answer to hold for several minutes before acting.
type Presence struct {
	MAC           string `json:"mac"`
	Present       bool   `json:"present"`
	StableSeconds int    `json:"stable_seconds,omitempty"`
}

type Webhook struct {
	// SecretHash is persisted/exported but stripped from ordinary API views.
	SecretHash string `json:"secret_hash,omitempty"`
	HasSecret  bool   `json:"has_secret,omitempty"`
}

type Condition struct {
	DeviceID string `json:"device_id"`
	On       bool   `json:"on"`
}

type Action struct {
	DeviceID      string          `json:"device_id"`
	AutomationID  string          `json:"automation_id,omitempty"`
	Action        string          `json:"action"`
	Value         json.RawMessage `json:"value,omitempty"`
	DelaySeconds  int             `json:"delay_seconds,omitempty"`
	OffsetMinutes int             `json:"offset_minutes,omitempty"`
	When          []Condition     `json:"when,omitempty"`
}

func (a Action) request() control.Request {
	return control.Request{Action: a.Action, Value: a.Value}
}

type ActionResult struct {
	DeviceID     string `json:"device_id,omitempty"`
	AutomationID string `json:"automation_id,omitempty"`
	Action       string `json:"action"`
	OK           bool   `json:"ok"`
	Skipped      bool   `json:"skipped,omitempty"`
	Error        string `json:"error,omitempty"`
}

type Run struct {
	ID            string         `json:"id"`
	RuleID        string         `json:"rule_id"`
	RuleName      string         `json:"rule_name"`
	Source        string         `json:"source"`
	OffsetMinutes int            `json:"offset_minutes"`
	StartedAt     time.Time      `json:"started_at"`
	DurationMS    int64          `json:"duration_ms"`
	OK            bool           `json:"ok"`
	Results       []ActionResult `json:"results"`
}

type Snapshot struct {
	State
	Runs []Run `json:"runs"`
	// Presence reports whether this host can read its neighbour table. Without
	// it a presence rule cannot be armed and is refused on save, so the app is
	// told here rather than at the end of a form.
	Presence bool `json:"presence"`
}

var safeActions = map[string]struct{}{
	"on": {}, "off": {}, "set_brightness": {}, "set_color": {},
	"set_color_temp": {}, "set_scene": {}, "set_scene_speed": {},
	"set_speed": {}, "set_sleep": {}, "set_timer": {}, "set_light": {},
	"set_volume": {}, "launch_app": {}, "wake": {}, ActionAutomation: {},
}

// capabilities reports whether a device advertises a capability. Capabilities
// are the vocabulary the whole app shares, so a trigger checks them rather than
// type-asserting the driver.
func hasCapability(dev device.Device, capability string) bool {
	for _, reported := range dev.Capabilities() {
		if reported == capability {
			return true
		}
	}
	return false
}

func validateState(state State, mgr *manager.Manager, presence bool) error {
	if state.Version != FormatVersion {
		return fmt.Errorf("automation version must be %d", FormatVersion)
	}
	if len(state.Items) > MaxRules {
		return fmt.Errorf("at most %d automations are allowed", MaxRules)
	}

	seen := make(map[string]struct{}, len(state.Items))
	for i := range state.Items {
		rule := &state.Items[i]
		if !idPattern.MatchString(rule.ID) {
			return fmt.Errorf("automation %d has an invalid id", i+1)
		}
		if _, duplicate := seen[rule.ID]; duplicate {
			return fmt.Errorf("duplicate automation id %q", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if len(rule.Name) < 1 || len(rule.Name) > 64 {
			return fmt.Errorf("automation %q name must be 1-64 characters", rule.ID)
		}
		if rule.CooldownSeconds < 0 || rule.CooldownSeconds > 3600 {
			return fmt.Errorf("automation %q cooldown must be 0-3600 seconds", rule.ID)
		}
		if err := validateTrigger(rule.ID, rule.Enabled, rule.Trigger, mgr, presence); err != nil {
			return err
		}
		if err := validateConditions(rule.ID, "condition", rule.Conditions, rule.Enabled, mgr); err != nil {
			return err
		}
		if len(rule.Actions) == 0 || len(rule.Actions) > MaxActions {
			return fmt.Errorf("automation %q must have 1-%d actions", rule.ID, MaxActions)
		}
		for _, action := range rule.Actions {
			if action.DelaySeconds < 0 || action.DelaySeconds > MaxDelay {
				return fmt.Errorf("automation %q action delay must be 0-%d seconds", rule.ID, MaxDelay)
			}
			if len(action.Value) > 1024 {
				return fmt.Errorf("automation %q action value is too large", rule.ID)
			}
			if action.OffsetMinutes < 0 || action.OffsetMinutes > MaxOffsetMinutes {
				return fmt.Errorf("automation %q action offset must be 0-%d minutes", rule.ID, MaxOffsetMinutes)
			}
			if action.OffsetMinutes > 0 && rule.Trigger.Type != TriggerSchedule {
				return fmt.Errorf("automation %q action offsets require a schedule trigger", rule.ID)
			}
			if err := validateConditions(rule.ID, "action condition", action.When, rule.Enabled, mgr); err != nil {
				return err
			}
			if _, allowed := safeActions[action.Action]; !allowed {
				return fmt.Errorf("automation %q action %q is not safe for automatic execution", rule.ID, action.Action)
			}
			if action.Action == ActionAutomation {
				if !idPattern.MatchString(action.AutomationID) {
					return fmt.Errorf("automation %q has an invalid nested automation id", rule.ID)
				}
				if action.DeviceID != "" || len(action.Value) != 0 {
					return fmt.Errorf("automation %q nested action must contain only an automation id", rule.ID)
				}
				continue
			}
			if action.AutomationID != "" {
				return fmt.Errorf("automation %q device action cannot reference another automation", rule.ID)
			}
			if !rule.Enabled {
				continue
			}
			dev, ok := mgr.Device(action.DeviceID)
			if !ok {
				return fmt.Errorf("automation %q action references unknown device %q", rule.ID, action.DeviceID)
			}
			if err := control.Validate(dev, action.request()); err != nil {
				return fmt.Errorf("automation %q action for %q: %w", rule.ID, action.DeviceID, err)
			}
		}
		if rule.Trigger.Type == TriggerSchedule {
			hasTimedStep := false
			hasStartStep := false
			for _, action := range rule.Actions {
				hasTimedStep = hasTimedStep || action.OffsetMinutes > 0
				hasStartStep = hasStartStep || action.OffsetMinutes == 0
			}
			if hasTimedStep && !hasStartStep {
				return fmt.Errorf("automation %q timed schedule needs an action at offset 0", rule.ID)
			}
		}
	}
	if err := validateAutomationCalls(state.Items); err != nil {
		return err
	}
	return validateFeedbackLoops(state.Items)
}

func validateConditions(ruleID, label string, conditions []Condition, enabled bool, mgr *manager.Manager) error {
	if len(conditions) > MaxConditions {
		return fmt.Errorf("automation %q has too many %ss", ruleID, label)
	}
	if !enabled {
		return nil
	}
	for _, condition := range conditions {
		dev, ok := mgr.Device(condition.DeviceID)
		if !ok {
			return fmt.Errorf("automation %q %s references unknown device %q", ruleID, label, condition.DeviceID)
		}
		if _, ok := dev.(device.Switchable); !ok {
			return fmt.Errorf("automation %q %s device %q has no power state", ruleID, label, condition.DeviceID)
		}
	}
	return nil
}

func validateAutomationCalls(rules []Rule) error {
	byID := make(map[string]Rule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}
	graph := make(map[string][]string)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		for _, action := range rule.Actions {
			if action.Action != ActionAutomation {
				continue
			}
			target, ok := byID[action.AutomationID]
			if !ok {
				return fmt.Errorf("automation %q references unknown automation %q", rule.ID, action.AutomationID)
			}
			if !target.Enabled {
				return fmt.Errorf("automation %q references disabled automation %q", rule.ID, action.AutomationID)
			}
			graph[rule.ID] = append(graph[rule.ID], target.ID)
		}
	}

	visiting := make(map[string]bool)
	depths := make(map[string]int)
	var walk func(string) (int, error)
	walk = func(id string) (int, error) {
		if visiting[id] {
			return 0, fmt.Errorf("nested automations contain a cycle")
		}
		if depth := depths[id]; depth > 0 {
			return depth, nil
		}
		visiting[id] = true
		depth := 1
		for _, next := range graph[id] {
			childDepth, err := walk(next)
			if err != nil {
				return 0, err
			}
			if childDepth+1 > depth {
				depth = childDepth + 1
			}
		}
		visiting[id] = false
		depths[id] = depth
		return depth, nil
	}
	for id := range graph {
		depth, err := walk(id)
		if err != nil {
			return err
		}
		if depth > MaxNesting {
			return fmt.Errorf("nested automation chain exceeds %d rules", MaxNesting)
		}
	}
	return nil
}

// exactlyOne rejects a trigger carrying a payload that does not belong to its
// type. Each trigger type owns one field, and the others must be absent.
func exactlyOne(trigger Trigger, want string) bool {
	payloads := [...]struct {
		kind    string
		present bool
	}{
		{TriggerSchedule, trigger.Schedule != nil},
		{TriggerDeviceState, trigger.Device != nil},
		{TriggerDeviceOffline, trigger.Offline != nil},
		{TriggerDeviceOnline, trigger.Online != nil},
		{TriggerPresence, trigger.Presence != nil},
		{TriggerWebhook, trigger.Webhook != nil},
	}
	found := false
	for _, payload := range payloads {
		if !payload.present {
			continue
		}
		if payload.kind != want {
			return false
		}
		found = true
	}
	return found
}

func validateTrigger(ruleID string, enabled bool, trigger Trigger, mgr *manager.Manager, presence bool) error {
	switch trigger.Type {
	case TriggerSchedule:
		if !exactlyOne(trigger, TriggerSchedule) {
			return fmt.Errorf("automation %q has an invalid schedule trigger", ruleID)
		}
		if _, err := time.Parse("15:04", trigger.Schedule.Time); err != nil {
			return fmt.Errorf("automation %q schedule time must be HH:MM", ruleID)
		}
		if len(trigger.Schedule.Weekdays) == 0 || len(trigger.Schedule.Weekdays) > 7 {
			return fmt.Errorf("automation %q schedule needs 1-7 weekdays", ruleID)
		}
		seen := [7]bool{}
		for _, day := range trigger.Schedule.Weekdays {
			if day < 0 || day > 6 || seen[day] {
				return fmt.Errorf("automation %q has invalid weekdays", ruleID)
			}
			seen[day] = true
		}
		if trigger.Schedule.UTCOffsetMinutes < -720 || trigger.Schedule.UTCOffsetMinutes > 840 {
			return fmt.Errorf("automation %q timezone offset is invalid", ruleID)
		}
	case TriggerDeviceState:
		if !exactlyOne(trigger, TriggerDeviceState) {
			return fmt.Errorf("automation %q has an invalid device trigger", ruleID)
		}
		watch := trigger.Device
		if watch.StableSeconds < 0 || watch.StableSeconds > MaxStableSeconds {
			return fmt.Errorf("automation %q stable time must be 0-%d seconds", ruleID, MaxStableSeconds)
		}
		if !watch.power() {
			if _, known := metricCapability[watch.Metric]; !known {
				return fmt.Errorf("automation %q watches unknown metric %q", ruleID, watch.Metric)
			}
			if watch.Operator != OpAbove && watch.Operator != OpBelow && watch.Operator != OpEquals {
				return fmt.Errorf("automation %q comparison must be %q, %q or %q", ruleID, OpAbove, OpBelow, OpEquals)
			}
			if watch.Value < 0 || watch.Value > MaxMetricValue {
				return fmt.Errorf("automation %q compared value must be 0-%d", ruleID, MaxMetricValue)
			}
		}
		if !enabled {
			return nil
		}
		dev, ok := mgr.Device(watch.DeviceID)
		if !ok {
			return fmt.Errorf("automation %q trigger references unknown device %q", ruleID, watch.DeviceID)
		}
		if watch.power() {
			if _, ok := dev.(device.Switchable); !ok {
				return fmt.Errorf("automation %q trigger device %q has no power state", ruleID, watch.DeviceID)
			}
			return nil
		}
		if !hasCapability(dev, metricCapability[watch.Metric]) {
			return fmt.Errorf("automation %q trigger device %q does not report %s", ruleID, watch.DeviceID, watch.Metric)
		}
	case TriggerDeviceOffline:
		if !exactlyOne(trigger, TriggerDeviceOffline) {
			return fmt.Errorf("automation %q has an invalid offline trigger", ruleID)
		}
		if trigger.Offline.Minutes < 1 || trigger.Offline.Minutes > MaxOfflineMinutes {
			return fmt.Errorf("automation %q offline time must be 1-%d minutes", ruleID, MaxOfflineMinutes)
		}
		if !enabled {
			return nil
		}
		dev, ok := mgr.Device(trigger.Offline.DeviceID)
		if !ok {
			return fmt.Errorf("automation %q trigger references unknown device %q", ruleID, trigger.Offline.DeviceID)
		}
		// Polling is not by itself a reachability signal: a device's Online can
		// carry a different meaning (e.g. a Samsung TV's Wake-on-LAN
		// controllability). Require a driver that explicitly opts in, either
		// because Online already means live contact or because it supplies that
		// signal separately (see device.LiveOnline).
		if !device.ReportsReachability(dev) {
			return fmt.Errorf("automation %q trigger device %q cannot report whether it is reachable", ruleID, trigger.Offline.DeviceID)
		}
	case TriggerDeviceOnline:
		if !exactlyOne(trigger, TriggerDeviceOnline) {
			return fmt.Errorf("automation %q has an invalid online trigger", ruleID)
		}
		if trigger.Online.Minutes < 1 || trigger.Online.Minutes > MaxOfflineMinutes {
			return fmt.Errorf("automation %q online recovery time must be 1-%d minutes", ruleID, MaxOfflineMinutes)
		}
		switch trigger.Online.Operator {
		case OpAbove, OpBelow, OpEquals:
		default:
			return fmt.Errorf("automation %q online recovery has an invalid comparison", ruleID)
		}
		if !enabled {
			return nil
		}
		dev, ok := mgr.Device(trigger.Online.DeviceID)
		if !ok {
			return fmt.Errorf("automation %q trigger references unknown device %q", ruleID, trigger.Online.DeviceID)
		}
		if !device.ReportsReachability(dev) {
			return fmt.Errorf("automation %q trigger device %q cannot report whether it is reachable", ruleID, trigger.Online.DeviceID)
		}
	case TriggerPresence:
		if !exactlyOne(trigger, TriggerPresence) {
			return fmt.Errorf("automation %q has an invalid presence trigger", ruleID)
		}
		if _, err := resolver.NormalizeMAC(trigger.Presence.MAC); err != nil {
			return fmt.Errorf("automation %q presence trigger needs a MAC address", ruleID)
		}
		if trigger.Presence.StableSeconds < 0 || trigger.Presence.StableSeconds > MaxPresenceStableSeconds {
			return fmt.Errorf("automation %q stable time must be 0-%d seconds", ruleID, MaxPresenceStableSeconds)
		}
		if enabled && !presence {
			return fmt.Errorf("automation %q needs LAN presence, which this host cannot read", ruleID)
		}
	case TriggerWebhook:
		if !exactlyOne(trigger, TriggerWebhook) {
			return fmt.Errorf("automation %q has an invalid webhook trigger", ruleID)
		}
		if trigger.Webhook.SecretHash != "" {
			decoded, err := hex.DecodeString(trigger.Webhook.SecretHash)
			if err != nil || len(decoded) != 32 {
				return fmt.Errorf("automation %q has an invalid webhook secret", ruleID)
			}
		}
	default:
		return fmt.Errorf("automation %q has unknown trigger type %q", ruleID, trigger.Type)
	}
	return nil
}

// disableInvalidRules reconciles operational rules with the devices available
// in this boot. Structural validation still runs afterwards; only rules that
// were enabled and can no longer bind safely are made inert.
func disableInvalidRules(state *State, mgr *manager.Manager, presence bool) []string {
	var disabled []string
	for {
		byID := make(map[string]Rule, len(state.Items))
		for _, rule := range state.Items {
			byID[rule.ID] = rule
		}
		changed := false
		for i := range state.Items {
			if !state.Items[i].Enabled {
				continue
			}
			if err := ruleBindingError(state.Items[i], byID, mgr, presence); err != nil {
				state.Items[i].Enabled = false
				disabled = append(disabled, state.Items[i].ID)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return disabled
}

func ruleBindingError(rule Rule, rules map[string]Rule, mgr *manager.Manager, presence bool) error {
	if err := validateTrigger(rule.ID, true, rule.Trigger, mgr, presence); err != nil {
		return err
	}
	for _, condition := range rule.Conditions {
		dev, ok := mgr.Device(condition.DeviceID)
		if !ok {
			return fmt.Errorf("missing condition device")
		}
		if _, ok := dev.(device.Switchable); !ok {
			return fmt.Errorf("condition device has no power state")
		}
	}
	for _, action := range rule.Actions {
		for _, condition := range action.When {
			dev, ok := mgr.Device(condition.DeviceID)
			if !ok {
				return fmt.Errorf("missing action condition device")
			}
			if _, ok := dev.(device.Switchable); !ok {
				return fmt.Errorf("action condition device has no power state")
			}
		}
		if action.Action == ActionAutomation {
			target, ok := rules[action.AutomationID]
			if !ok || !target.Enabled {
				return fmt.Errorf("nested automation is unavailable")
			}
			continue
		}
		dev, ok := mgr.Device(action.DeviceID)
		if !ok {
			return fmt.Errorf("missing action device")
		}
		if err := control.Validate(dev, action.request()); err != nil {
			return err
		}
	}
	return nil
}

// effect is one device value an action can change, with the value it writes
// when that can be read from the action.
type effect struct {
	deviceID string
	metric   string
	value    int
	// known distinguishes "writes 40" from "writes something we could not read".
	// An unreadable value is treated as able to match anything.
	known bool
}

// retriggers reports whether this effect could satisfy a trigger watching the
// same value.
//
// Power stays deliberately coarse: any on/off is treated as able to feed any
// power trigger, which is the guarantee this validation has always given. A
// numeric action writes a value we can read, so it is compared exactly — "dim
// to 40% when it goes above 70%" settles at 40 and cannot run again, and
// refusing it would rule out a whole class of useful rules for no reason.
func (e effect) retriggers(trigger DeviceTrigger) bool {
	if e.metric == MetricPower || !e.known {
		return true
	}
	switch trigger.Operator {
	case OpAbove:
		return e.value > trigger.Value
	case OpBelow:
		return e.value < trigger.Value
	case OpEquals:
		return e.value == trigger.Value
	}
	return true
}

// actionEffects reports which watchable values an action can move, and to what.
//
// Several actions move more than the obvious one: on most hardware, setting a
// brightness or a fan speed also switches the device on. Listing that here is
// what stops "dim the lamp when it turns on" plus "turn the lamp off when it
// dims" from being accepted as a pair.
func actionEffects(action Action) []effect {
	written, ok := actionNumber(action)
	numeric := func(metric string) effect {
		return effect{metric: metric, value: written, known: ok}
	}
	power := effect{metric: MetricPower}
	switch action.Action {
	case "on", "off", "wake":
		return []effect{power}
	case "set_brightness":
		return []effect{numeric(MetricBrightness), power}
	case "set_speed":
		return []effect{numeric(MetricSpeed), power}
	case "set_volume":
		return []effect{numeric(MetricVolume)}
	case "set_color_temp":
		return []effect{numeric(MetricColorTemp)}
	case "set_timer":
		return []effect{numeric(MetricTimerHours)}
	}
	return nil
}

// actionNumber reads the literal an action writes. Validation has already
// accepted the action, so an unreadable value here means a shape this check
// cannot reason about — reported as unknown, never as zero.
func actionNumber(action Action) (int, bool) {
	var value int
	if err := json.Unmarshal(action.Value, &value); err != nil {
		return 0, false
	}
	return value, true
}

// Reject rule sets whose device-state triggers and actions form a feedback loop:
// a chain where running one rule can retrigger a rule that leads back to it.
//
// Only device-state triggers take part. A schedule and a webhook come from
// outside; a presence trigger watches a MAC no action can move; offline and
// online-recovery triggers each fire once per unreachable episode and need a
// new reachability transition to rearm, so none can be driven round a loop by
// an action.
func validateFeedbackLoops(rules []Rule) error {
	byID := make(map[string]Rule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}
	// Which rules watch which device value.
	type watched struct{ deviceID, metric string }
	watchers := make(map[watched][]Rule)
	for _, rule := range rules {
		if !rule.Enabled || rule.Trigger.Type != TriggerDeviceState || rule.Trigger.Device == nil {
			continue
		}
		key := watched{deviceID: rule.Trigger.Device.DeviceID, metric: rule.Trigger.Device.metric()}
		watchers[key] = append(watchers[key], rule)
	}

	memo := make(map[string][]effect)
	graph := make(map[string][]string)
	for _, rule := range rules {
		if !rule.Enabled || rule.Trigger.Type != TriggerDeviceState || rule.Trigger.Device == nil {
			continue
		}
		seen := make(map[string]bool)
		for _, caused := range nestedEffects(rule, byID, memo) {
			for _, target := range watchers[watched{deviceID: caused.deviceID, metric: caused.metric}] {
				if seen[target.ID] || !caused.retriggers(*target.Trigger.Device) {
					continue
				}
				seen[target.ID] = true
				graph[rule.ID] = append(graph[rule.ID], target.ID)
			}
		}
	}

	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(node string) bool {
		if visiting[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visiting[node] = true
		for _, next := range graph[node] {
			if visit(next) {
				return true
			}
		}
		visiting[node] = false
		visited[node] = true
		return false
	}
	for node := range graph {
		if visit(node) {
			return fmt.Errorf("these automations would retrigger each other in a loop")
		}
	}
	return nil
}

// nestedEffects treats an inline automation call as part of its caller.
// validateAutomationCalls has already made the enabled call graph acyclic and
// bounded, so this small recursive walk cannot loop indefinitely.
func nestedEffects(rule Rule, byID map[string]Rule, memo map[string][]effect) []effect {
	if effects, ok := memo[rule.ID]; ok {
		return effects
	}
	// Guard the walk itself, not just its result: a call graph that is still
	// being validated may not be acyclic yet.
	memo[rule.ID] = nil
	var effects []effect
	seen := make(map[effect]bool)
	add := func(item effect) {
		if !seen[item] {
			seen[item] = true
			effects = append(effects, item)
		}
	}
	for _, action := range rule.Actions {
		if action.Action == ActionAutomation {
			if target, ok := byID[action.AutomationID]; ok && target.Enabled {
				if target.Trigger.Type == TriggerSchedule {
					target.Actions = actionsAtOffset(target.Actions, 0)
				}
				for _, item := range nestedEffects(target, byID, memo) {
					add(item)
				}
			}
			continue
		}
		for _, caused := range actionEffects(action) {
			caused.deviceID = action.DeviceID
			add(caused)
		}
	}
	memo[rule.ID] = effects
	return effects
}

func cloneState(state State) State {
	out := state
	out.Items = make([]Rule, len(state.Items))
	for i, rule := range state.Items {
		out.Items[i] = rule
		out.Items[i].Conditions = append([]Condition(nil), rule.Conditions...)
		out.Items[i].Actions = make([]Action, len(rule.Actions))
		for j, action := range rule.Actions {
			out.Items[i].Actions[j] = action
			out.Items[i].Actions[j].Value = append(json.RawMessage(nil), action.Value...)
			out.Items[i].Actions[j].When = append([]Condition(nil), action.When...)
		}
		if rule.Trigger.Schedule != nil {
			schedule := *rule.Trigger.Schedule
			schedule.Weekdays = append([]int(nil), schedule.Weekdays...)
			out.Items[i].Trigger.Schedule = &schedule
		}
		if rule.Trigger.Device != nil {
			deviceTrigger := *rule.Trigger.Device
			out.Items[i].Trigger.Device = &deviceTrigger
		}
		if rule.Trigger.Offline != nil {
			offline := *rule.Trigger.Offline
			out.Items[i].Trigger.Offline = &offline
		}
		if rule.Trigger.Online != nil {
			online := *rule.Trigger.Online
			out.Items[i].Trigger.Online = &online
		}
		if rule.Trigger.Presence != nil {
			presence := *rule.Trigger.Presence
			out.Items[i].Trigger.Presence = &presence
		}
		if rule.Trigger.Webhook != nil {
			webhook := *rule.Trigger.Webhook
			out.Items[i].Trigger.Webhook = &webhook
		}
	}
	if out.Items == nil {
		out.Items = []Rule{}
	}
	return out
}

func publicState(state State) State {
	out := cloneState(state)
	for i := range out.Items {
		if webhook := out.Items[i].Trigger.Webhook; webhook != nil {
			webhook.HasSecret = webhook.SecretHash != ""
			webhook.SecretHash = ""
		}
	}
	return out
}
