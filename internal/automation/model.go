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
)

const (
	FormatVersion = 1
	MaxRules      = 64
	MaxConditions = 4
	MaxActions    = 16
	MaxDelay      = 60
)

const (
	TriggerSchedule    = "schedule"
	TriggerDeviceState = "device_state"
	TriggerWebhook     = "webhook"
)

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
	Type     string         `json:"type"`
	Schedule *Schedule      `json:"schedule,omitempty"`
	Device   *DeviceTrigger `json:"device,omitempty"`
	Webhook  *Webhook       `json:"webhook,omitempty"`
}

type Schedule struct {
	Time             string `json:"time"`     // HH:MM
	Weekdays         []int  `json:"weekdays"` // 0=Sunday ... 6=Saturday
	UTCOffsetMinutes int    `json:"utc_offset_minutes"`
}

type DeviceTrigger struct {
	DeviceID      string `json:"device_id"`
	On            bool   `json:"on"`
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
	DeviceID     string          `json:"device_id"`
	Action       string          `json:"action"`
	Value        json.RawMessage `json:"value,omitempty"`
	DelaySeconds int             `json:"delay_seconds,omitempty"`
}

func (a Action) request() control.Request {
	return control.Request{Action: a.Action, Value: a.Value}
}

type ActionResult struct {
	DeviceID string `json:"device_id"`
	Action   string `json:"action"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

type Run struct {
	ID         string         `json:"id"`
	RuleID     string         `json:"rule_id"`
	RuleName   string         `json:"rule_name"`
	Source     string         `json:"source"`
	StartedAt  time.Time      `json:"started_at"`
	DurationMS int64          `json:"duration_ms"`
	OK         bool           `json:"ok"`
	Results    []ActionResult `json:"results"`
}

type Snapshot struct {
	State
	Runs []Run `json:"runs"`
}

var safeActions = map[string]struct{}{
	"on": {}, "off": {}, "set_brightness": {}, "set_color": {},
	"set_color_temp": {}, "set_scene": {}, "set_scene_speed": {},
	"set_volume": {}, "launch_app": {}, "wake": {},
}

func validateState(state State, mgr *manager.Manager) error {
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
		if err := validateTrigger(rule.ID, rule.Enabled, rule.Trigger, mgr); err != nil {
			return err
		}
		if len(rule.Conditions) > MaxConditions {
			return fmt.Errorf("automation %q has too many conditions", rule.ID)
		}
		for _, condition := range rule.Conditions {
			if !rule.Enabled {
				continue
			}
			dev, ok := mgr.Device(condition.DeviceID)
			if !ok {
				return fmt.Errorf("automation %q condition references unknown device %q", rule.ID, condition.DeviceID)
			}
			if _, ok := dev.(device.Switchable); !ok {
				return fmt.Errorf("automation %q condition device %q has no power state", rule.ID, condition.DeviceID)
			}
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
			if _, allowed := safeActions[action.Action]; !allowed {
				return fmt.Errorf("automation %q action %q is not safe for automatic execution", rule.ID, action.Action)
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
	}
	return validatePowerCycles(state.Items)
}

func validateTrigger(ruleID string, enabled bool, trigger Trigger, mgr *manager.Manager) error {
	switch trigger.Type {
	case TriggerSchedule:
		if trigger.Schedule == nil || trigger.Device != nil || trigger.Webhook != nil {
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
		if trigger.Device == nil || trigger.Schedule != nil || trigger.Webhook != nil {
			return fmt.Errorf("automation %q has an invalid device trigger", ruleID)
		}
		if trigger.Device.StableSeconds < 0 || trigger.Device.StableSeconds > 300 {
			return fmt.Errorf("automation %q stable time must be 0-300 seconds", ruleID)
		}
		if !enabled {
			return nil
		}
		dev, ok := mgr.Device(trigger.Device.DeviceID)
		if !ok {
			return fmt.Errorf("automation %q trigger references unknown device %q", ruleID, trigger.Device.DeviceID)
		}
		if _, ok := dev.(device.Switchable); !ok {
			return fmt.Errorf("automation %q trigger device %q has no power state", ruleID, trigger.Device.DeviceID)
		}
	case TriggerWebhook:
		if trigger.Webhook == nil || trigger.Schedule != nil || trigger.Device != nil {
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
func disableInvalidRules(state *State, mgr *manager.Manager) []string {
	var disabled []string
	for i := range state.Items {
		if !state.Items[i].Enabled {
			continue
		}
		single := State{Version: state.Version, Items: []Rule{state.Items[i]}}
		if err := validateState(single, mgr); err != nil {
			state.Items[i].Enabled = false
			disabled = append(disabled, state.Items[i].ID)
		}
	}
	return disabled
}

// Reject cycles made from power-changing device relations. Non-power actions
// cannot retrigger an on/off edge and therefore do not belong in this graph.
func validatePowerCycles(rules []Rule) error {
	graph := make(map[string][]string)
	for _, rule := range rules {
		if !rule.Enabled || rule.Trigger.Type != TriggerDeviceState || rule.Trigger.Device == nil {
			continue
		}
		from := rule.Trigger.Device.DeviceID
		for _, action := range rule.Actions {
			if action.Action == "on" || action.Action == "off" {
				graph[from] = append(graph[from], action.DeviceID)
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
			return fmt.Errorf("device power relations contain a cycle")
		}
	}
	return nil
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
