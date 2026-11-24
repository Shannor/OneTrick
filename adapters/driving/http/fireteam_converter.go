package httpadapter

import (
	"oneTrick/api"
	"oneTrick/core/fireteam"
)

func ToAPIFireteamMembers(members []fireteam.Member) []api.FireteamMember {
	out := make([]api.FireteamMember, 0, len(members))
	for _, m := range members {
		chars := make([]api.Character, 0, len(m.Characters))
		for _, c := range m.Characters {
			chars = append(chars, ToAPICharacter(c))
		}
		active := make([]api.FireteamActiveSession, 0, len(m.ActiveSessions))
		for _, ses := range m.ActiveSessions {
			active = append(active, api.FireteamActiveSession{
				SessionID:   ses.SessionID,
				CharacterID: ses.CharacterID,
				StartedAt:   ses.StartedAt,
				TrackComp:   new(ses.TrackComp),
			})
		}
		out = append(out, api.FireteamMember{
			ID:             m.ID,
			MembershipID:   m.MembershipID,
			DisplayName:    m.DisplayName,
			Characters:     chars,
			ActiveSessions: new(active),
		})
	}
	return out
}

func ToAPICharacter(c fireteam.Character) api.Character {
	var statsPtr *map[string]api.ClassStat
	if c.Stats != nil {
		stats := make(map[string]api.ClassStat, len(c.Stats))
		for k, st := range c.Stats {
			stats[k] = api.ClassStat{
				Name:            st.Name,
				Description:     st.Description,
				Icon:            st.Icon,
				HasIcon:         st.HasIcon,
				StatCategory:    st.StatCategory,
				AggregationType: st.AggregationType,
				Value:           st.Value,
			}
		}
		statsPtr = &stats
	}

	return api.Character{
		Id:                  c.ID,
		Class:               c.Class,
		Race:                c.Race,
		CurrentTitle:        c.CurrentTitle,
		Light:               c.Light,
		EmblemURL:           c.EmblemURL,
		EmblemBackgroundURL: c.EmblemBackgroundURL,
		EmblemColor: api.Color{
			Red:   c.EmblemColor.Red,
			Green: c.EmblemColor.Green,
			Blue:  c.EmblemColor.Blue,
			Alpha: c.EmblemColor.Alpha,
		},
		Stats: statsPtr,
	}
}
