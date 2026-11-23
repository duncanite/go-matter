package fabric_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/spec"
	acfdef "github.com/SukramJ/go-fabric/cluster/spec/activatedcarbonfiltermonitoring"
	descriptordef "github.com/SukramJ/go-fabric/cluster/spec/descriptor"
	hepadef "github.com/SukramJ/go-fabric/cluster/spec/hepafiltermonitoring"
	pm25def "github.com/SukramJ/go-fabric/cluster/spec/pm25concentrationmeasurement"
	thermostatdef "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	"github.com/SukramJ/go-fabric/controller/client"
	"github.com/SukramJ/go-fabric/fabrictest"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/mattertest"
)

// thermostat reads a heater's thermostat over the wire.
func (r *rig) thermostat(t *testing.T, endpoint uint16) thermostatdef.State {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, endpoint, thermostatdef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	state, _ := thermostatdef.StateFrom(values)

	return state
}

// part is the part of a device that is of a device type, found among
// the parts its endpoint lists.
func (r *rig) part(t *testing.T, owner uint16, kind uint32) uint16 {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, owner, descriptordef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	whole, _ := descriptordef.StateFrom(values)

	for _, part := range whole.PartsList {
		if slices.Contains(r.kinds(t, part), kind) {
			return part
		}
	}

	t.Fatalf("the device at %d has no part of the type %#x among %v", owner, kind, whole.PartsList)

	return 0
}

// TestAPurifierIsComposed: an air purifier with everything is one
// bridged device, its sensors and its thermostat parts of it and of the
// types Matter composes one with, its filters on its own endpoint.
func TestAPurifierIsComposed(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Heater(&home, "purifier")

	served := serve(t, &home)
	purifier := served.endpoint(t, "d:purifier")

	values, err := fabrictest.ReadCluster(t.Context(), served.pair, purifier, descriptordef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	whole, _ := descriptordef.StateFrom(values)
	if len(whole.PartsList) != 4 {
		t.Fatalf("the purifier has the parts %v", whole.PartsList)
	}

	// A part is no bridged device of its own.
	for name, kind := range map[string]uint32{
		"temperature sensor": 0x0302, "humidity sensor": 0x0307, "air quality sensor": 0x002C, "thermostat": 0x0301,
	} {
		if kinds := served.kinds(t, served.part(t, purifier, kind)); slices.Contains(kinds, 0x0013) {
			t.Errorf("the %s is of the types %#x", name, kinds)
		}
	}

	// Its filters: the one that says how much is left has a condition,
	// the one that does not has none.
	if values, err = fabrictest.ReadCluster(t.Context(), served.pair, purifier, hepadef.ClusterID); err != nil {
		t.Fatal(err)
	}

	if hepa, absent := hepadef.StateFrom(
		values,
	); hepa.Condition != 80 ||
		slices.Contains(absent, hepadef.AttrCondition) ||
		hepa.DegradationDirection != hepadef.DegradationDirectionDown ||
		hepa.ChangeIndication != hepadef.ChangeIndicationOk {
		t.Errorf("the HEPA filter is served as %+v, without %v", hepa, absent)
	}

	if values, err = fabrictest.ReadCluster(t.Context(), served.pair, purifier, acfdef.ClusterID); err != nil {
		t.Fatal(err)
	}

	if _, absent := acfdef.StateFrom(values); !slices.Contains(absent, acfdef.AttrCondition) {
		t.Error("a filter that says nothing of what is left is served with a condition")
	}

	// Its particulates are by weight, in air.
	air := served.part(t, purifier, 0x002C)
	if values, err = fabrictest.ReadCluster(t.Context(), served.pair, air, pm25def.ClusterID); err != nil {
		t.Fatal(err)
	}

	if fine, _ := pm25def.StateFrom(values); fine.MeasuredValue == nil || *fine.MeasuredValue != 8 ||
		fine.MeasurementUnit != pm25def.MeasurementUnitUgm3 || fine.MeasurementMedium != pm25def.MeasurementMediumAir {
		t.Errorf("the fine particulates are served as %+v", fine)
	}

	// Unreachable, it is so as one device.
	if err = home.Report("purifier", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the purifier unreachable", func() bool { return !served.reachable(t, purifier) })
}

// TestAHeaterIsAThermostat: a heater is a thermostat that only heats, at
// the device's own temperature, and what a controller sets on it is what
// the heater is asked.
func TestAHeaterIsAThermostat(t *testing.T) {
	t.Parallel()

	var home matter.Home

	heater := mattertest.Heater(&home, "purifier")
	served := serve(t, &home)
	at := served.part(t, served.endpoint(t, "d:purifier"), 0x0301)

	is := served.thermostat(t, at)
	if is.LocalTemperature == nil || *is.LocalTemperature != 2150 || is.OccupiedHeatingSetpoint != 2000 ||
		is.MinHeatSetpointLimit != 100 || is.MaxHeatSetpointLimit != 3700 ||
		is.AbsMinHeatSetpointLimit != 100 || is.AbsMaxHeatSetpointLimit != 3700 ||
		is.SystemMode != thermostatdef.SystemModeOff || is.ThermostatRunningState != 0 ||
		is.ControlSequenceOfOperation != thermostatdef.ControlSequenceOfOperationHeatingOnly {
		t.Fatalf("the heater is served as %+v", is)
	}

	// Its temperature is the device's, known or not.
	cold := 12.25

	if err := home.Report("purifier", matter.TemperatureChanged{Celsius: &cold}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the heater at the device's temperature", func() bool {
		now := served.thermostat(t, at)

		return now.LocalTemperature != nil && *now.LocalTemperature == 1225
	})

	if err := home.Report("purifier", matter.TemperatureChanged{}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the heater at no temperature", func() bool {
		return served.thermostat(t, at).LocalTemperature == nil
	})

	// Heating now is the thermostat's relay.
	if err := home.Report("purifier", matter.HeaterChanged{On: true, Heating: true, Target: 20}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the heater heating", func() bool {
		now := served.thermostat(t, at)

		return now.SystemMode == thermostatdef.SystemModeHeat &&
			now.ThermostatRunningState == thermostatdef.RelayStateHeat
	})

	// A setpoint raised is the heater's target raised, by tenths.
	before := len(heater.Asked())

	if status := served.status(
		t,
		at,
		thermostatdef.ClusterID,
		thermostatdef.CmdSetpointRaiseLower,
		thermostatdef.SetpointRaiseLowerRequest{
			Mode:   thermostatdef.SetpointRaiseLowerModeHeat,
			Amount: 15,
		},
	); status != 0 {
		t.Fatalf("raising the setpoint was answered %#x", status)
	}

	if asked := heater.Asked(); len(asked) != before+1 ||
		!reflect.DeepEqual(asked[before], mattertest.SetHeatTarget{Celsius: 21.5}) {
		t.Fatalf("raising the setpoint reached the heater as %+v", asked[before:])
	}

	// A mode that is neither off nor heat is refused, and asks nothing:
	// one Matter lets a thermostat that heats be set to.
	before = len(heater.Asked())

	err := fabrictest.Write(t.Context(), served.pair,
		client.AttributeAt(at, thermostatdef.ClusterID, thermostatdef.AttrSystemMode),
		spec.Uint(thermostatdef.SystemModeEmergencyHeat))

	if _, refused := errors.AsType[*fabrictest.StatusError](err); !refused {
		t.Fatalf("a heater set to heat in an emergency was answered %v", err)
	}

	if len(heater.Asked()) != before {
		t.Fatalf("a heater set to heat in an emergency was asked %+v", heater.Asked()[before:])
	}
}
