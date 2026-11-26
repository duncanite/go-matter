package fabric

import (
	"context"
	"fmt"
	"math"

	acfdef "thingfabric.dev/go-fabric/cluster/spec/activatedcarbonfiltermonitoring"
	airqualitydef "thingfabric.dev/go-fabric/cluster/spec/airquality"
	hepadef "thingfabric.dev/go-fabric/cluster/spec/hepafiltermonitoring"
	pm10def "thingfabric.dev/go-fabric/cluster/spec/pm10concentrationmeasurement"
	pm25def "thingfabric.dev/go-fabric/cluster/spec/pm25concentrationmeasurement"
	humiditydef "thingfabric.dev/go-fabric/cluster/spec/relativehumiditymeasurement"
	temperaturedef "thingfabric.dev/go-fabric/cluster/spec/temperaturemeasurement"
	thermostatdef "thingfabric.dev/go-fabric/cluster/spec/thermostat"
	"thingfabric.dev/go-fabric/contract"
	"thingfabric.dev/go-fabric/device"

	"github.com/duncanite/go-matter"
)

const (
	// Matter's temperatures and humidities are in hundredths, of a degree
	// and of a percent; a setpoint is raised and lowered by tenths of a
	// degree.
	hundredths = 100
	tenths     = 10
	// What a part of a device is kept under: the part's name is before
	// the ID, which may hold anything.
	partKey = "p:"
)

// aired is what an air purifier is served with beside its fan: each
// server is nil for a device that has no such thing.
type aired struct {
	temperature *temperaturedef.Server
	humidity    *humiditydef.Server
	quality     *airqualitydef.Server
	pm25        *pm25def.Server
	pm10        *pm10def.Server
	hepa        *hepadef.Server
	carbon      *acfdef.Server
	thermostat  *thermostatdef.Server
}

// hundredth is a reading in Matter's hundredths.
func hundredth(reading float64) int16 {
	return int16(math.Round(reading * hundredths))
}

// filters are an air purifier's filters as its endpoint declares them:
// how much is left where the filter says, and whether it is due.
func (h *served) filters() []device.Declaration {
	var declared []device.Declaration

	for _, filter := range h.device.Filters {
		left, due, down := conditionOf(filter)

		if filter.Kind == matter.HEPA {
			direction, indication := hepadef.DegradationDirectionEnum(down), hepadef.ChangeIndicationEnum(due)
			declare := hepadef.Declare{NoFeatures: true, Initial: hepadef.Patch{ChangeIndication: &indication}}

			if left != nil {
				declare.NoFeatures, declare.Features = false, hepadef.FeatureCondition
				declare.Initial.Condition, declare.Initial.DegradationDirection = left, &direction
			}

			declared = append(declared, declare)

			continue
		}

		direction, indication := acfdef.DegradationDirectionEnum(down), acfdef.ChangeIndicationEnum(due)
		declare := acfdef.Declare{NoFeatures: true, Initial: acfdef.Patch{ChangeIndication: &indication}}

		if left != nil {
			declare.NoFeatures, declare.Features = false, acfdef.FeatureCondition
			declare.Initial.Condition, declare.Initial.DegradationDirection = left, &direction
		}

		declared = append(declared, declare)
	}

	return declared
}

// conditionOf is a filter in Matter's words, the same for either kind:
// its condition in percent, nil when it says none; whether it is due,
// as ok or critical; and that a condition goes down as the filter wears.
func conditionOf(filter matter.Filter) (left *uint8, due, down uint8) {
	if filter.Left != nil {
		percent := uint8(math.Round(*filter.Left * percentFull))
		left = &percent
	}

	if filter.Replace {
		due = uint8(hepadef.ChangeIndicationCritical)
	}

	return left, due, uint8(hepadef.DegradationDirectionDown)
}

// compose makes what an air purifier has beside its fan and its filters
// parts of its endpoint, as Matter composes one: a temperature sensor, a
// humidity sensor, an air quality sensor and a thermostat that only
// heats, each for a device that has it.
func (h *served) compose(purifier *device.AirPurifier) error {
	h.air.hepa, h.air.carbon = purifier.HepaFilterMonitoring(), purifier.ActivatedCarbonFilterMonitoring()

	parts := []func() (*device.Device, error){h.thermometer, h.hygrometer, h.sniffer, h.heater}

	for _, build := range parts {
		part, err := build()
		if err != nil {
			return err
		}

		if part == nil {
			continue
		}

		if err = purifier.AddPart(part); err != nil {
			return fmt.Errorf("composing its endpoint: %w", err)
		}
	}

	return nil
}

// part is the options of a part of the device.
func (h *served) part(name string) device.Options {
	return device.Options{StableKey: device.StringKey(partKey + name + ":" + string(h.device.ID))}
}

// thermometer is the device's temperature sensor.
func (h *served) thermometer() (*device.Device, error) {
	reads := h.device.Temperature
	if reads == nil {
		return nil, nil //nolint:nilnil // a device with no temperature has no such part, and that is no error
	}

	made, err := device.NewTemperatureSensor(
		h.part("temperature"),
		temperaturedef.Declare{Initial: temperatureOf(reads.Celsius)},
	)
	if err != nil {
		return nil, fmt.Errorf("making its temperature sensor: %w", err)
	}

	h.air.temperature = made.TemperatureMeasurement()

	return made.Device, nil
}

// temperatureOf is a temperature as its cluster holds it: null when it
// is not known.
func temperatureOf(celsius *float64) temperaturedef.Patch {
	if celsius == nil {
		return temperaturedef.Patch{Null: []uint32{temperaturedef.AttrMeasuredValue}}
	}

	measured := hundredth(*celsius)

	return temperaturedef.Patch{MeasuredValue: &measured}
}

// hygrometer is the device's humidity sensor.
func (h *served) hygrometer() (*device.Device, error) {
	reads := h.device.Humidity
	if reads == nil {
		return nil, nil //nolint:nilnil // a device with no humidity has no such part, and that is no error
	}

	made, err := device.NewHumiditySensor(h.part("humidity"), humiditydef.Declare{Initial: humidityOf(reads.Percent)})
	if err != nil {
		return nil, fmt.Errorf("making its humidity sensor: %w", err)
	}

	h.air.humidity = made.RelativeHumidityMeasurement()

	return made.Device, nil
}

// humidityOf is a humidity as its cluster holds it.
func humidityOf(percent *float64) humiditydef.Patch {
	if percent == nil {
		return humiditydef.Patch{Null: []uint32{humiditydef.AttrMeasuredValue}}
	}

	measured := uint16(math.Round(*percent * hundredths))

	return humiditydef.Patch{MeasuredValue: &measured}
}

// sniffer is the device's air quality sensor, with the particulates it
// counts.
func (h *served) sniffer() (*device.Device, error) {
	reads := h.device.AirQuality
	if reads == nil {
		return nil, nil //nolint:nilnil // a device with no air quality has no such part, and that is no error
	}

	quality := qualityOf(reads.Quality)
	declared := []device.Declaration{airqualitydef.Declare{
		Features: airqualitydef.FeatureFair | airqualitydef.FeatureModerate |
			airqualitydef.FeatureVeryPoor | airqualitydef.FeatureExtremelyPoor,
		Initial: airqualitydef.Patch{AirQuality: &quality},
	}}

	if reads.PM25 != nil {
		unit, medium := pm25def.MeasurementUnitUgm3, pm25def.MeasurementMediumAir
		initial := fineOf(reads.PM25.Micrograms)
		initial.MeasurementUnit, initial.MeasurementMedium = &unit, &medium

		declared = append(declared, pm25def.Declare{Features: pm25def.FeatureNumericMeasurement, Initial: initial})
	}

	if reads.PM10 != nil {
		unit, medium := pm10def.MeasurementUnitUgm3, pm10def.MeasurementMediumAir
		initial := coarseOf(reads.PM10.Micrograms)
		initial.MeasurementUnit, initial.MeasurementMedium = &unit, &medium

		declared = append(declared, pm10def.Declare{Features: pm10def.FeatureNumericMeasurement, Initial: initial})
	}

	made, err := device.NewAirQualitySensor(h.part("air"), declared...)
	if err != nil {
		return nil, fmt.Errorf("making its air quality sensor: %w", err)
	}

	h.air.quality = made.AirQuality()
	h.air.pm25, h.air.pm10 = made.Pm25ConcentrationMeasurement(), made.Pm10ConcentrationMeasurement()

	return made.Device, nil
}

// qualityOf is a quality on Matter's scale, which is the vocabulary's
// own: one that is none is one that is not known.
func qualityOf(quality matter.Quality) airqualitydef.AirQualityEnum {
	if quality < matter.QualityUnknown || quality > matter.QualityExtremelyPoor {
		return airqualitydef.AirQualityUnknown
	}

	return airqualitydef.AirQualityEnum(quality) // #nosec G115 -- held to Matter's seven just above
}

// fineOf and coarseOf are a count of particulates as the cluster of its
// size holds it: null when it is not known.
func fineOf(micrograms *float64) pm25def.Patch {
	if micrograms == nil {
		return pm25def.Patch{Null: []uint32{pm25def.AttrMeasuredValue}}
	}

	measured := float32(*micrograms)

	return pm25def.Patch{MeasuredValue: &measured}
}

func coarseOf(micrograms *float64) pm10def.Patch {
	if micrograms == nil {
		return pm10def.Patch{Null: []uint32{pm10def.AttrMeasuredValue}}
	}

	measured := float32(*micrograms)

	return pm10def.Patch{MeasuredValue: &measured}
}

// heater is the device's heater: a thermostat that only heats, at the
// device's own temperature when it reads one.
func (h *served) heater() (*device.Device, error) {
	heats := h.device.Heater
	if heats == nil {
		return nil, nil //nolint:nilnil // a device with no heater has no such part, and that is no error
	}

	least, most := hundredth(heats.Least), hundredth(heats.Most)
	sequence := thermostatdef.ControlSequenceOfOperationHeatingOnly

	initial := heaterOf(heats)
	initial.AbsMinHeatSetpointLimit, initial.MinHeatSetpointLimit = &least, &least
	initial.AbsMaxHeatSetpointLimit, initial.MaxHeatSetpointLimit = &most, &most
	initial.ControlSequenceOfOperation = &sequence
	initial.Null = []uint32{thermostatdef.AttrLocalTemperature}

	if reads := h.device.Temperature; reads != nil && reads.Celsius != nil {
		room := hundredth(*reads.Celsius)
		initial.LocalTemperature, initial.Null = &room, nil
	}

	made, err := device.NewThermostat(h.part("heater"), thermostatdef.Declare{
		Features: thermostatdef.FeatureHeating,
		Initial:  initial,
		OnWrite:  h.heatWritten,
		HandleSetpointRaiseLower: func(ctx context.Context, req thermostatdef.SetpointRaiseLowerRequest) (struct{}, error) {
			heats := h.now().Heater

			return struct{}{}, crossing(heats.SetTarget(ctx, heats.Target+float64(req.Amount)/tenths))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("making its thermostat: %w", err)
	}

	h.air.thermostat = made.Thermostat()

	return made.Device, nil
}

// heaterOf is how a heater stands as its thermostat holds it.
func heaterOf(heats *matter.Heater) thermostatdef.Patch {
	mode, running, target := thermostatdef.SystemModeOff, thermostatdef.RelayStateBitmap(0), hundredth(heats.Target)

	if heats.On {
		mode = thermostatdef.SystemModeHeat
	}

	if heats.Heating {
		running = thermostatdef.RelayStateHeat
	}

	return thermostatdef.Patch{SystemMode: &mode, ThermostatRunningState: &running, OccupiedHeatingSetpoint: &target}
}

// heatWritten is what a controller wrote to the thermostat, before it is
// kept: a mode is the heater set to heat or not, a setpoint its target.
// What the device refuses is not kept.
func (h *served) heatWritten(ctx context.Context, next thermostatdef.State, attribute uint32) error {
	heats := h.now().Heater

	switch attribute {
	case thermostatdef.AttrSystemMode:
		if next.SystemMode != thermostatdef.SystemModeOff && next.SystemMode != thermostatdef.SystemModeHeat {
			return contract.StatusError{Status: statusConstraint, Message: "the heater is off, or heats"}
		}

		return crossing(heats.SetOn(ctx, next.SystemMode == thermostatdef.SystemModeHeat))
	case thermostatdef.AttrOccupiedHeatingSetpoint:
		return crossing(heats.SetTarget(ctx, float64(next.OccupiedHeatingSetpoint)/hundredths))
	default:
		return contract.StatusError{Status: statusUnsupported, Message: "the heater has no such setting"}
	}
}

// aired takes what the source says of an air purifier beyond its fan,
// and says it on the node. What the device cannot have is refused.
func (h *served) aired(change matter.Change) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	described := &h.device

	switch change := change.(type) {
	case matter.TemperatureChanged:
		if described.Temperature == nil || h.air.temperature == nil {
			return errNoSuchState
		}

		described.Temperature = &matter.Temperature{Celsius: change.Celsius}

		if err := h.air.temperature.Set(temperatureOf(change.Celsius)); err != nil {
			return fmt.Errorf("saying its temperature: %w", err)
		}

		return h.roomed(change.Celsius)
	case matter.HumidityChanged:
		if described.Humidity == nil || h.air.humidity == nil {
			return errNoSuchState
		}

		described.Humidity = &matter.Humidity{Percent: change.Percent}

		if err := h.air.humidity.Set(humidityOf(change.Percent)); err != nil {
			return fmt.Errorf("saying its humidity: %w", err)
		}
	case matter.AirQualityChanged:
		return h.sniffed(change)
	case matter.FilterChanged:
		return h.filtered(change)
	case matter.HeaterChanged:
		return h.heated(change)
	}

	return nil
}

// heated is aired for the heater. The lock is held.
func (h *served) heated(change matter.HeaterChanged) error {
	if h.device.Heater == nil || h.air.thermostat == nil {
		return errNoSuchState
	}

	// A new heater, never the one in place written into: a copy of the
	// device a command took shares it.
	heats := *h.device.Heater
	heats.On, heats.Heating, heats.Target = change.On, change.Heating, change.Target
	h.device.Heater = &heats

	if err := h.air.thermostat.Set(heaterOf(&heats)); err != nil {
		return fmt.Errorf("saying its heater: %w", err)
	}

	return nil
}

// roomed says the device's temperature on its thermostat, when it has
// one. The lock is held.
func (h *served) roomed(celsius *float64) error {
	if h.air.thermostat == nil {
		return nil
	}

	room := thermostatdef.Patch{Null: []uint32{thermostatdef.AttrLocalTemperature}}

	if celsius != nil {
		at := hundredth(*celsius)
		room = thermostatdef.Patch{LocalTemperature: &at}
	}

	if err := h.air.thermostat.Set(room); err != nil {
		return fmt.Errorf("saying its heater's temperature: %w", err)
	}

	return nil
}

// sniffed is aired for the quality of the air. The lock is held.
func (h *served) sniffed(change matter.AirQualityChanged) error {
	reads := h.device.AirQuality
	if reads == nil || h.air.quality == nil ||
		(change.PM25 != nil && reads.PM25 == nil) || (change.PM10 != nil && reads.PM10 == nil) {
		return errNoSuchState
	}

	now := matter.AirQuality{Quality: change.Quality}
	quality := qualityOf(change.Quality)

	if err := h.air.quality.Set(airqualitydef.Patch{AirQuality: &quality}); err != nil {
		return fmt.Errorf("saying its air quality: %w", err)
	}

	if reads.PM25 != nil {
		now.PM25 = &matter.Particulates{Micrograms: change.PM25}

		if err := h.air.pm25.Set(fineOf(change.PM25)); err != nil {
			return fmt.Errorf("saying its fine particulates: %w", err)
		}
	}

	if reads.PM10 != nil {
		now.PM10 = &matter.Particulates{Micrograms: change.PM10}

		if err := h.air.pm10.Set(coarseOf(change.PM10)); err != nil {
			return fmt.Errorf("saying its coarse particulates: %w", err)
		}
	}

	h.device.AirQuality = &now

	return nil
}

// filtered is aired for a filter. The lock is held.
func (h *served) filtered(change matter.FilterChanged) error {
	if change.Filter < 0 || change.Filter >= len(h.device.Filters) ||
		(change.Left == nil) != (h.device.Filters[change.Filter].Left == nil) {
		return errNoSuchState
	}

	// New filters, never the ones in place written into.
	filters := append([]matter.Filter(nil), h.device.Filters...)
	filters[change.Filter].Left, filters[change.Filter].Replace = change.Left, change.Replace
	h.device.Filters = filters

	left, due, _ := conditionOf(filters[change.Filter])

	var err error

	if filters[change.Filter].Kind == matter.HEPA {
		indication := hepadef.ChangeIndicationEnum(due)
		err = h.air.hepa.Set(hepadef.Patch{Condition: left, ChangeIndication: &indication})
	} else {
		indication := acfdef.ChangeIndicationEnum(due)
		err = h.air.carbon.Set(acfdef.Patch{Condition: left, ChangeIndication: &indication})
	}

	if err != nil {
		return fmt.Errorf("saying its filter: %w", err)
	}

	return nil
}
