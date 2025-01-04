package api

import (
	"log/slog"
	"oneTrick/clients/destiny"
	"oneTrick/utils"
)

func TransformItemToDetails(item *destiny.DestinyItem) *ItemDetails {
	if item == nil {
		return nil
	}
	result := ItemDetails{
		CharacterId: item.CharacterId,
	}

	if item.Item != nil {
		c := *item.Item.ItemComponent
		result.BaseInfo = &BaseItemInfo{
			BucketHash: utils.ToPointer(int(*c.BucketHash)),
			InstanceId: c.ItemInstanceId,
			ItemHash:   utils.ToPointer(int(*c.ItemHash)),
			Name:       utils.ToPointer("temp name"),
		}
	}
	for _, p := range *item.Perks.Data.Perks {
		if result.Perks == nil {
			result.Perks = &[]Perk{}
		}
		*result.Perks = append(*result.Perks, Perk{
			Hash:     utils.ToPointer(int(*p.PerkHash)),
			IconPath: p.IconPath,
			IsActive: p.IsActive,
			Visible:  p.Visible,
		})
	}
	for _, s := range *item.Sockets.Data.Sockets {
		if result.Sockets == nil {
			result.Sockets = &[]Socket{}
		}
		if s.PlugHash == nil {
			slog.Warn("Socket has no plug hash")
			continue
		}
		hash := int(*s.PlugHash)
		*result.Sockets = append(*result.Sockets, Socket{
			IsEnabled: s.IsEnabled,
			IsVisible: s.IsVisible,
			PlugHash:  &hash,
		})
	}

	stats := make(Stats)
	for key, s := range *item.Stats.Data.Stats {
		if s.StatHash == nil || s.Value == nil {
			slog.Warn("Missing stat hash or value for stat: ", key)
			continue
		}
		hash := int(*s.StatHash)
		value := int(*s.Value)

		stats[key] = struct {
			StatHash *int `json:"statHash,omitempty"`
			Value    *int `json:"value,omitempty"`
		}{StatHash: &hash, Value: &value}

	}
	result.Stats = &stats
	return &result
}

func TransformD2HistoricalStatValues(stats *map[string]destiny.HistoricalStatsValue) *map[string]StatsValue {
	if stats == nil {
		return nil
	}
	result := make(map[string]StatsValue)
	for key, value := range *stats {
		values := transformD2StatValue(&value)
		if values == nil {
			continue
		}
		result[key] = *values

	}
	return &result
}

func transformD2StatValue(item *destiny.HistoricalStatsValue) *StatsValue {
	if item == nil {
		return nil
	}
	result := &StatsValue{
		ActivityId: item.ActivityId,
		StatId:     item.StatId,
	}
	if item.Basic != nil {
		result.Basic = &StatsValuePair{
			DisplayValue: item.Basic.DisplayValue,
			Value:        item.Basic.Value,
		}
	}
	if item.Pga != nil {
		result.Pga = &StatsValuePair{
			DisplayValue: item.Pga.DisplayValue,
			Value:        item.Pga.Value,
		}
	}
	if item.Weighted != nil {
		result.Weighted = &StatsValuePair{
			DisplayValue: item.Weighted.DisplayValue,
			Value:        item.Weighted.Value,
		}
	}
	return result
}

func uintToInt64[T ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](item *T) *int64 {
	if item == nil {
		return nil
	}
	return utils.ToPointer(int64(*item))
}

func TransformHistoricActivity(history destiny.HistoricalStatsActivity) ActivityHistory {
	return ActivityHistory{
		ActivityHash: *uintToInt64(history.DirectorActivityHash),
		InstanceId:   *history.InstanceId,
		IsPrivate:    history.IsPrivate,
		Mode:         utils.ToPointer("Mode"),
		ReferenceId:  *uintToInt64(history.ReferenceId),
	}
}
func TransformPeriodGroups(period []destiny.StatsPeriodGroup) []ActivityHistory {
	if period == nil {
		return nil
	}
	result := []ActivityHistory{}
	for _, group := range period {
		result = append(result, *TransformPeriodGroup(&group))
	}
	return result
}
func TransformPeriodGroup(period *destiny.StatsPeriodGroup) *ActivityHistory {
	if period == nil {
		return nil
	}

	return &ActivityHistory{
		ActivityHash: *uintToInt64(period.ActivityDetails.DirectorActivityHash),
		InstanceId:   *period.ActivityDetails.InstanceId,
		IsPrivate:    period.ActivityDetails.IsPrivate,
		Mode:         utils.ToPointer("Mode"),
		ReferenceId:  *uintToInt64(period.ActivityDetails.ReferenceId),
	}
}
