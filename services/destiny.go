package services

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-resty/resty/v2"
	"log"
	"log/slog"
	"net/http"
	"oneTrick/clients/destiny"
	utils2 "oneTrick/utils"
	"os"
	"strconv"
	"time"
)

const apiKey = "e3a8403b8e274b438735bc9de80cd1db"
const searchUserPOST = "/User/Search/GlobalName/{page}/"
const getProfile = "/Destiny2/2/Profile/4611686018434106050/?components=206,205"
const characterID = "2305843009261519028"
const membershipId = "7274114"

// Reference ID == ItemHash
const referenceID = "882778888"

const Kinetic = 1498876634
const Energy = 2465295065
const Power = 953998645
const SubClass = 3284755031

type DestinyService interface {
	GetUserSnapshot(membershipID int64) ([]destiny.DestinyEntitiesItemsDestinyItemComponent, *time.Time, error)
	WriteToFile(items []destiny.DestinyEntitiesItemsDestinyItemComponent, timestamp *time.Time) error
	GetQuickPlayActivity(membershipID, characterID int64, count int64, page int64) (any, error)
	GetCompetitiveActivity(membershipID, characterID int64, count int64, page int64) (any, error)
}

type Service struct {
	client    *resty.Client
	genClient *destiny.ClientWithResponses
}

func (a *Service) GetQuickPlayActivity(membershipID, characterID int64, count int64, page int64) (any, error) {
	return getActivity(a, membershipID, characterID, count, int64(destiny.CurrentActivityModeTypePvPQuickplay), page)
}

func (a *Service) GetCompetitiveActivity(membershipID, characterID int64, count int64, page int64) (any, error) {
	return getActivity(a, membershipID, characterID, count, int64(destiny.CurrentActivityModeTypePvPCompetitive), page)
}

func getActivity(a *Service, membershipID, characterID int64, count int64, mode int64, page int64) (
	[]destiny.DestinyHistoricalStatsDestinyHistoricalStatsPeriodGroup,
	error,
) {
	resp, err := a.genClient.Destiny2GetActivityHistoryWithResponse(
		context.Background(),
		2,
		membershipID,
		characterID,
		&destiny.Destiny2GetActivityHistoryParams{
			Count: utils2.ToPointer(int32(count)),
			Mode:  utils2.ToPointer(int32(mode)),
			Page:  utils2.ToPointer(int32(page)),
		},
	)
	if err != nil {
		return nil, err
	}
	if resp.JSON200.Response.Activities == nil {
		return make([]destiny.DestinyHistoricalStatsDestinyHistoricalStatsPeriodGroup, 0), nil
	}
	return *resp.JSON200.Response.Activities, nil
}

const profileFile = "profile_data.json"

func (a *Service) WriteToFile(items []destiny.DestinyEntitiesItemsDestinyItemComponent, timestamp *time.Time) error {
	existingData := make(map[string]any)
	stat, err := os.Stat(profileFile)
	if err == nil && !stat.IsDir() {
		content, readErr := os.ReadFile(profileFile)
		if readErr == nil {
			_ = json.Unmarshal(content, &existingData) // Ignore error for simplicity
		} else {
			slog.With("error", readErr.Error()).Error("Failed to read file")
		}
	}

	file, err := os.OpenFile(profileFile, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to open file")
		return err
	}
	defer file.Close()

	// TODO: Format the timestamp
	existingData[timestamp.String()] = items
	// Marshal the updated data
	data, err := json.MarshalIndent(existingData, "", "  ")
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to marshal items")
		return err
	}

	if _, err := file.Write(data); err != nil {
		slog.With("error", err.Error()).Error("Failed to write to file")
		return err
	}
	return nil
}

func (a *Service) GetUserSnapshot(membershipId int64) ([]destiny.DestinyEntitiesItemsDestinyItemComponent, *time.Time, error) {
	var components []int32
	components = append(components, 205)
	params := &destiny.Destiny2GetProfileParams{
		Components: &components,
	}
	test, err := a.genClient.Destiny2GetProfileWithResponse(context.Background(), 2, membershipId, params)
	if err != nil {
		slog.With(err).Error(err.Error())
		return nil, nil, err
	}
	if test.JSON200 == nil {
		return nil, nil, fmt.Errorf("no response found")
	}
	timeStamp := test.JSON200.Response.ResponseMintedTimestamp
	var items []destiny.DestinyEntitiesItemsDestinyItemComponent
	if test.JSON200.Response.CharacterEquipment.Data != nil {
		equipment := *test.JSON200.Response.CharacterEquipment.Data
		for ID, equ := range equipment {
			if characterID == ID {
				items = *equ.Items
			}
		}

	}
	results := make([]destiny.DestinyEntitiesItemsDestinyItemComponent, 0)
	for _, item := range items {
		switch *item.BucketHash {
		case Kinetic:
			results = append(results, item)
		case Energy:
			results = append(results, item)
		case Power:
			results = append(results, item)
		case SubClass:
			results = append(results, item)
		}
	}
	utils2.PrettyPrint(len(results))
	return results, timeStamp, nil
}

const accessToken = "CPjuBhKGAgAgPaFF75otR0QMEd5aiJ9/Zwm9DEam9oZfHluU556o3mbgAAAACMiTGscENoFDeffOB30j3GhPHUhp1ZbXJsdzjOFhGLw8HFA7triZ5s0wx965nNXdn3IDxjBjxjd65Xg+2b6yM0cgRzQAnIhPy/uvq/oBT2s9lIkPKripHs5yCOmSbZXnOHLCOr0ZvN1Dx3aWBtXDd8bgZEJrAfmnTHnBsZhTWmHMLT6A8CoNJJHJiRLgAI0EsGcbYZDTAZzt+OVur1CLS+/F/yQnhNwKzP1cmVHnu02Zq2meNcQQazkxNUPEwFcxPRycTMXEHNQH0T0pbGvX0Q3FJe9OuNLS+5VyCvJPdpo="

func NewDestinyService() DestinyService {
	hc := http.Client{}
	cli, err := destiny.NewClientWithResponses(
		"https://www.bungie.net/Platform",
		destiny.WithHTTPClient(&hc),
		destiny.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Add("X-API-KEY", apiKey)
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", accessToken))
			req.Header.Add("Accept", "application/json")
			req.Header.Add("Content-Type", "application/json")
			req.Header.Add("User-Agent", "oneTrick-backend")
			return nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	c := resty.New()
	c.SetDebug(false)
	c.SetBaseURL("https://www.bungie.net/Platform")
	c.SetHeader("X-Service-KEY", apiKey)
	c.SetHeader("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	c.SetHeader("Accept", "application/json")
	c.SetHeaders(map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   "My custom User Agent String",
	})

	return &Service{
		client:    c,
		genClient: cli,
	}
}
func (a *Service) SearchUsers(searchTerm string, page int) (*UserResponse, error) {

	responseStruct := &UserResponse{}
	_, err := a.client.R().
		EnableTrace().
		SetResult(responseStruct).
		SetPathParams(map[string]string{
			"page": strconv.Itoa(page),
		}).
		SetBody(map[string]string{"displayNamePrefix": searchTerm}).
		Post(searchUserPOST)

	if err != nil {
		slog.With(err).Error(err.Error())
		return nil, err
	}

	return responseStruct, nil
}

type UserResponse struct {
	Response struct {
		SearchResults []struct {
			BungieGlobalDisplayName     string `json:"bungieGlobalDisplayName"`
			BungieGlobalDisplayNameCode int    `json:"bungieGlobalDisplayNameCode"`
			DestinyMemberships          []struct {
				IconPath                    string `json:"iconPath"`
				CrossSaveOverride           int    `json:"crossSaveOverride"`
				ApplicableMembershipTypes   []int  `json:"applicableMembershipTypes"`
				IsPublic                    bool   `json:"isPublic"`
				MembershipType              int    `json:"membershipType"`
				MembershipID                string `json:"membershipId"`
				DisplayName                 string `json:"displayName"`
				BungieGlobalDisplayName     string `json:"bungieGlobalDisplayName"`
				BungieGlobalDisplayNameCode int    `json:"bungieGlobalDisplayNameCode"`
			} `json:"destinyMemberships"`
			BungieNetMembershipID string `json:"bungieNetMembershipId,omitempty"`
		} `json:"searchResults"`
		Page    int  `json:"page"`
		HasMore bool `json:"hasMore"`
	} `json:"Response"`
	ErrorCode       int    `json:"ErrorCode"`
	ThrottleSeconds int    `json:"ThrottleSeconds"`
	ErrorStatus     string `json:"ErrorStatus"`
	Message         string `json:"Message"`
	MessageData     struct {
	} `json:"MessageData"`
}
