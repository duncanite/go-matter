package fabric

import (
	"context"
	"fmt"
	"slices"

	"thingfabric.dev/go-fabric/cluster/spec"
	acfdef "thingfabric.dev/go-fabric/cluster/spec/activatedcarbonfiltermonitoring"
	airqualitydef "thingfabric.dev/go-fabric/cluster/spec/airquality"
	descriptordef "thingfabric.dev/go-fabric/cluster/spec/descriptor"
	hepadef "thingfabric.dev/go-fabric/cluster/spec/hepafiltermonitoring"
	pm10def "thingfabric.dev/go-fabric/cluster/spec/pm10concentrationmeasurement"
	pm25def "thingfabric.dev/go-fabric/cluster/spec/pm25concentrationmeasurement"
	humiditydef "thingfabric.dev/go-fabric/cluster/spec/relativehumiditymeasurement"
	temperaturedef "thingfabric.dev/go-fabric/cluster/spec/temperaturemeasurement"
	thermostatdef "thingfabric.dev/go-fabric/cluster/spec/thermostat"
	"thingfabric.dev/go-fabric/controller/client"

	"github.com/duncanite/go-matter"
)

// The device types an air purifier is composed with.
const (
	typeAirQualitySensor  uint32 = 0x002C
	typeThermostat        uint32 = 0x0301
	typeTemperatureSensor uint32 = 0x0302
	typeHumiditySensor    uint32 = 0x0307
)

// air is what an air purifier has beside its fan: its filters, on its
// own endpoint, and what its parts are, each read where Matter has it.
func (c *Controller) air(held *client.Node, endpoint *client.Endpoint, described *matter.Device) {
	if cluster := endpoint.Cluster(hepadef.ClusterID); cluster != nil {
		said, absent := hepadef.StateFrom(cluster)
		described.Filters = append(described.Filters, filterOf(
			matter.HEPA, said.Condition, slices.Contains(absent, hepadef.AttrCondition),
			said.ChangeIndication == hepadef.ChangeIndicationCritical,
		))
	}

	if cluster := endpoint.Cluster(acfdef.ClusterID); cluster != nil {
		said, absent := acfdef.StateFrom(cluster)
		described.Filters = append(described.Filters, filterOf(
			matter.Carbon, said.Condition, slices.Contains(absent, acfdef.AttrCondition),
			said.ChangeIndication == acfdef.ChangeIndicationCritical,
		))
	}

	for _, part := range endpoint.Parts() {
		is, _ := descriptordef.StateFrom(part.Cluster(descriptordef.ClusterID))

		for _, listed := range is.DeviceTypeList {
			switch listed.DeviceType {
			case typeTemperatureSensor:
				said, _ := temperaturedef.StateFrom(part.Cluster(temperaturedef.ClusterID))
				described.Temperature = &matter.Temperature{Celsius: fromHundredths(said.MeasuredValue)}
			case typeHumiditySensor:
				said, _ := humiditydef.StateFrom(part.Cluster(humiditydef.ClusterID))
				described.Humidity = &matter.Humidity{}

				if said.MeasuredValue != nil {
					percent := float64(*said.MeasuredValue) / hundredths
					described.Humidity.Percent = &percent
				}
			case typeAirQualitySensor:
				described.AirQuality = sniffed(part)
			case typeThermostat:
				described.Heater = c.heater(held, part)
			}
		}
	}
}

// filterOf is a filter as its cluster says it: how much is left where
// the cluster has a condition, and whether it is due.
func filterOf(kind matter.FilterKind, condition uint8, unsaid, due bool) matter.Filter {
	filter := matter.Filter{Kind: kind, Replace: due}

	if !unsaid {
		left := min(float64(condition)/percentFull, 1)
		filter.Left = &left
	}

	return filter
}

// fromHundredths is a temperature of Matter's as a reading: nil for one
// that is null.
func fromHundredths(measured *int16) *float64 {
	if measured == nil {
		return nil
	}

	celsius := float64(*measured) / hundredths

	return &celsius
}

// sniffed is an air quality sensor's endpoint as the vocabulary has it:
// the quality, and the particulates it has a cluster for.
func sniffed(part *client.Endpoint) *matter.AirQuality {
	said, _ := airqualitydef.StateFrom(part.Cluster(airqualitydef.ClusterID))
	air := &matter.AirQuality{Quality: matter.Quality(said.AirQuality)}

	if air.Quality < matter.QualityUnknown || air.Quality > matter.QualityExtremelyPoor {
		air.Quality = matter.QualityUnknown
	}

	if cluster := part.Cluster(pm25def.ClusterID); cluster != nil {
		fine, _ := pm25def.StateFrom(cluster)
		air.PM25 = &matter.Particulates{Micrograms: counted(fine.MeasuredValue)}
	}

	if cluster := part.Cluster(pm10def.ClusterID); cluster != nil {
		coarse, _ := pm10def.StateFrom(cluster)
		air.PM10 = &matter.Particulates{Micrograms: counted(coarse.MeasuredValue)}
	}

	return air
}

// counted is a count of particulates of Matter's as a reading: nil for
// one that is null, or that is no count.
func counted(measured *float32) *float64 {
	if measured == nil || !(*measured >= 0) {
		return nil
	}

	micrograms := float64(*measured)

	return &micrograms
}

// heater is a thermostat's endpoint as a heater: one that heats, or is
// off.
func (c *Controller) heater(held *client.Node, part *client.Endpoint) *matter.Heater {
	said, _ := thermostatdef.StateFrom(part.Cluster(thermostatdef.ClusterID))
	number := part.Number()

	heats := &matter.Heater{
		On:      said.SystemMode != thermostatdef.SystemModeOff,
		Heating: said.ThermostatRunningState&thermostatdef.RelayStateHeat != 0,
		Target:  float64(said.OccupiedHeatingSetpoint) / hundredths,
		Least:   float64(said.MinHeatSetpointLimit) / hundredths,
		Most:    float64(said.MaxHeatSetpointLimit) / hundredths,
	}

	least, most := heats.Least, heats.Most

	heats.SetOn = func(ctx context.Context, on bool) error {
		mode := thermostatdef.SystemModeOff
		if on {
			mode = thermostatdef.SystemModeHeat
		}

		return c.write(ctx, held, number, thermostatdef.ClusterID, thermostatdef.AttrSystemMode, spec.Uint(mode))
	}

	heats.SetTarget = func(ctx context.Context, celsius float64) error {
		if !(celsius >= least && celsius <= most) {
			return fmt.Errorf("%w: the target %v, outside %v to %v", matter.ErrInvalid, celsius, least, most)
		}

		return c.write(ctx, held, number, thermostatdef.ClusterID, thermostatdef.AttrOccupiedHeatingSetpoint,
			spec.Int(hundredth(celsius)))
	}

	return heats
}

// airShape is what a device has beside its fan, which does not change
// while the device is the same: which readings, which particulates,
// which filters and whether each says how much is left, and a heater
// with its range.
func airShape(described *matter.Device) string {
	shape := []any{described.Temperature != nil, described.Humidity != nil, described.AirQuality != nil}

	if air := described.AirQuality; air != nil {
		shape = append(shape, air.PM25 != nil, air.PM10 != nil)
	}

	for _, filter := range described.Filters {
		shape = append(shape, filter.Kind, filter.Left != nil)
	}

	if heats := described.Heater; heats != nil {
		shape = append(shape, heats.Least, heats.Most)
	}

	return fmt.Sprint(shape...)
}

// airChanges is how what a device has beside its fan stands, as the changes
// that say it.
func airChanges(described *matter.Device) []matter.Change {
	var changes []matter.Change

	if reads := described.Temperature; reads != nil {
		changes = append(changes, matter.TemperatureChanged{Celsius: reads.Celsius})
	}

	if reads := described.Humidity; reads != nil {
		changes = append(changes, matter.HumidityChanged{Percent: reads.Percent})
	}

	if air := described.AirQuality; air != nil {
		change := matter.AirQualityChanged{Quality: air.Quality}

		if air.PM25 != nil {
			change.PM25 = air.PM25.Micrograms
		}

		if air.PM10 != nil {
			change.PM10 = air.PM10.Micrograms
		}

		changes = append(changes, change)
	}

	for index, filter := range described.Filters {
		changes = append(changes, matter.FilterChanged{Filter: index, Left: filter.Left, Replace: filter.Replace})
	}

	if heats := described.Heater; heats != nil {
		changes = append(changes, matter.HeaterChanged{On: heats.On, Heating: heats.Heating, Target: heats.Target})
	}

	return changes
}
