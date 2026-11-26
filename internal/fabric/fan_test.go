package fabric_test

import (
	"testing"
	"time"

	fancontroldef "thingfabric.dev/go-fabric/cluster/spec/fancontrol"
	"thingfabric.dev/go-fabric/fabrictest"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/mattertest"
)

// fan reads a fan's own cluster over the wire.
func (r *rig) fan(t *testing.T, endpoint uint16) fancontroldef.State {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, endpoint, fancontroldef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	state, _ := fancontroldef.StateFrom(values)

	return state
}

// TestAFanLeftToItselfSaysNoSpeed: on the wire, a fan that is on says
// the speed it is set to, in percent and in steps; one left to itself
// says none of either, as Matter has it, where a controller would show
// a speed nobody set; and one that is off says zero.
func TestAFanLeftToItselfSaysNoSpeed(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Fan(&home, "fan")

	served := serve(t, &home)
	endpoint := served.endpoint(t, "d:fan")

	stands := func(what string, mode matter.FanMode, holds func(fancontroldef.State) bool) {
		t.Helper()

		if err := home.Report(
			"fan",
			matter.FanChanged{Mode: mode, Speed: 0.5, Running: mode != matter.FanOff},
		); err != nil {
			t.Fatal(err)
		}

		var said fancontroldef.State

		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if said = served.fan(t, endpoint); holds(said) {
				return
			}
		}

		t.Fatalf("%s: the fan says %+v", what, said)
	}

	stands("on at half", matter.FanOn, func(said fancontroldef.State) bool {
		return said.FanMode == fancontroldef.FanModeHigh &&
			said.PercentSetting != nil && *said.PercentSetting == 50 &&
			said.SpeedSetting != nil && *said.SpeedSetting != 0
	})

	stands("left to itself", matter.FanAuto, func(said fancontroldef.State) bool {
		return said.FanMode == fancontroldef.FanModeAuto && said.PercentSetting == nil && said.SpeedSetting == nil
	})

	stands("off", matter.FanOff, func(said fancontroldef.State) bool {
		return said.FanMode == fancontroldef.FanModeOff &&
			said.PercentSetting != nil && *said.PercentSetting == 0 && said.PercentCurrent == 0
	})
}
