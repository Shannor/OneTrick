package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/utils"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/algolia/algoliasearch-client-go/v4/algolia/search"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/iterator"
)

type Service interface {
	GetUser(ctx context.Context, ID string) (*User, error)
	CreateUser(ctx context.Context, user *User) (*User, error)
	GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error)
	GetFireteam(ctx context.Context, userID string) ([]api.FireteamMember, error)
	// BackfillCharacters fetches characters from Destiny for the user's primary membership
	// and updates the user's characterIds field in Firestore.
	BackfillCharacters(ctx context.Context, userID string) error
	UpdateCharacters(ctx context.Context, userID string, characters []api.Character) error
	// GetAll returns all users. Used for admin backfills.
	GetAll(ctx context.Context) ([]User, error)
	// GetByCharacterID returns the user that owns the provided characterID. If not found returns (nil, nil).
	GetByCharacterID(ctx context.Context, characterID string) (*User, error)
	UpdateUserSearch(ctx context.Context) error
	Search(ctx context.Context, query string, page int) ([]api.SearchUserResult, error)
}
type userService struct {
	db           *firestore.Client
	d2Service    destiny.Service
	searchClient *search.APIClient
}

var _ Service = (*userService)(nil)

const (
	userCollection         = "users"
	characterSubCollection = "characters"
)

func NewUserService(client *firestore.Client, service destiny.Service, searchClient *search.APIClient) Service {
	return &userService{
		db:           client,
		d2Service:    service,
		searchClient: searchClient,
	}
}

var NotFound = errors.New("user not found")

func (s *userService) GetUser(ctx context.Context, ID string) (*User, error) {
	user := User{}

	q1 := firestore.PropertyFilter{
		Path:     "id",
		Operator: "==",
		Value:    ID,
	}

	q2 := firestore.PropertyFilter{
		Path:     "memberId",
		Operator: "==",
		Value:    ID,
	}
	q3 := firestore.PropertyFilter{
		Path:     "primaryMembershipId",
		Operator: "==",
		Value:    ID,
	}
	orFilter := firestore.OrFilter{
		Filters: []firestore.EntityFilter{q1, q2, q3},
	}

	iter := s.db.Collection(userCollection).WhereEntity(orFilter).Documents(ctx)

	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		err = doc.DataTo(&user)
		if err != nil {
			return nil, err
		}
		return &user, nil
	}

	return nil, NotFound
}

func (s *userService) CreateUser(ctx context.Context, user *User) (*User, error) {
	if user == nil {
		return nil, errors.New("user is nil")
	}

	now := time.Now()
	user.CreatedAt = now

	ref := s.db.Collection(userCollection).NewDoc()
	user.ID = ref.ID

	_, err := ref.Set(ctx, user)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *userService) GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch user: %w", err)
	}
	membershipType := int64(0)
	for _, membership := range u.Memberships {
		if membership.ID == membershipID {
			membershipType = membership.Type
		}
	}
	return membershipType, nil
}

func (s *userService) GetFireteam(ctx context.Context, userID string) ([]api.FireteamMember, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	t := int64(0)
	for _, membership := range u.Memberships {
		if membership.ID == u.PrimaryMembershipID {
			t = membership.Type
			break
		}
	}
	pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse primary membership id")
	}
	partyMembers, err := s.d2Service.GetPartyMembers(ctx, pmId, t)
	if err != nil {
		if errors.Is(err, destiny.ErrDestinyServerDown) {
			return nil, fmt.Errorf("destiny 3rd party service is down")
		}
		return nil, fmt.Errorf("failed to fetch characters: %w", err)
	}

	fireteam := make([]api.FireteamMember, 0)
	for _, member := range partyMembers {
		if member.MembershipId == nil {
			log.Warn().Msg("missing membership id for party member")
			continue
		}
		member, err := s.GetUser(ctx, *member.MembershipId)
		if err != nil {
			// TODO: Add a case here for telling people to join one trick
			continue
		}
		fireteam = append(fireteam, api.FireteamMember{
			DisplayName:  member.DisplayName,
			ID:           member.ID,
			MembershipID: member.PrimaryMembershipID,
		})
	}

	return fireteam, nil
}

// BackfillCharacters fetches the user's characters from Destiny and updates the
// characterIds array on the User document. This is useful for users created before
// character IDs were persisted or when data needs to be refreshed.
func (s *userService) BackfillCharacters(ctx context.Context, userID string) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to fetch user: %w", err)
	}
	if u.PrimaryMembershipID == "" {
		return fmt.Errorf("user missing primary membership id")
	}
	// Find membership type for the primary membership
	membershipType := int64(0)
	for _, m := range u.Memberships {
		if m.ID == u.PrimaryMembershipID {
			membershipType = m.Type
			break
		}
	}
	pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse primary membership id: %w", err)
	}
	chars, err := s.d2Service.GetCharacters(ctx, pmId, membershipType)
	if err != nil {
		return fmt.Errorf("failed to fetch characters: %w", err)
	}
	charIDs := make([]string, 0, len(chars))
	for _, c := range chars {
		charIDs = append(charIDs, c.Id)
	}
	_, err = s.db.Collection(userCollection).Doc(u.ID).Set(ctx, map[string]any{
		"characters":            chars,
		"characterIds":          charIDs,
		"lastUpdatedCharacters": time.Now(),
	}, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to update user character ids: %w", err)
	}
	return nil
}

func (s *userService) UpdateCharacters(ctx context.Context, userID string, characters []api.Character) error {
	charIDs := make([]string, 0, len(characters))
	for _, c := range characters {
		charIDs = append(charIDs, c.Id)
	}
	_, err := s.db.Collection(userCollection).Doc(userID).Set(ctx, map[string]any{
		"characterIds":          charIDs,
		"characters":            characters,
		"lastUpdatedCharacters": time.Now(),
	}, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to update user characters: %w", err)
	}
	return nil
}

// GetAll returns all users in the system. Intended for admin backfills.
func (s *userService) GetAll(ctx context.Context) ([]User, error) {
	docs, err := s.db.Collection(userCollection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[User](docs)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetByCharacterID returns the user that owns the provided characterID. If not found returns (nil, nil).
func (s *userService) GetByCharacterID(ctx context.Context, characterID string) (*User, error) {
	if characterID == "" {
		return nil, nil
	}
	q := s.db.Collection(userCollection).
		Where("characterIds", "array-contains", characterID).
		Limit(1)
	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, nil
	}
	u := &User{}
	if err := docs[0].DataTo(u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *userService) UpdateUserSearch(ctx context.Context) error {
	users, err := s.GetAll(ctx)
	if err != nil {
		return err
	}
	u := make([]map[string]any, 0)
	for _, user := range users {
		alternateNames := make([]string, 0)
		for _, membership := range user.Memberships {
			alternateNames = append(alternateNames, membership.DisplayName)
		}
		u = append(u, map[string]any{
			"objectID":            user.ID,
			"displayName":         user.DisplayName,
			"uniqueName":          user.UniqueName,
			"alternateNames":      alternateNames,
			"bungieID":            user.MemberID,
			"primaryMembershipID": user.PrimaryMembershipID,
		})
	}
	// push data to algolia
	result, err := s.searchClient.SaveObjects("user_index", u)
	if err != nil {
		return err
	}
	fmt.Printf("Done! Uploaded records in %d batches.", len(result))
	return nil
}

func (s *userService) Search(ctx context.Context, query string, page int) ([]api.SearchUserResult, error) {
	searchParams := search.SearchParams{
		SearchParamsObject: search.
			NewEmptySearchParamsObject().
			SetQuery(query),
	}
	response, err := s.searchClient.SearchSingleIndex(
		s.searchClient.NewApiSearchSingleIndexRequest("user_index").WithSearchParams(&searchParams),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search algolia: %w", err)
	}
	results := make([]api.SearchUserResult, 0, len(response.Hits))

	for _, hit := range response.Hits {
		var result api.SearchUserResult
		// Marshal to JSON then unmarshal to struct
		jsonData, err := json.Marshal(hit)
		if err != nil {
			continue // or handle error appropriately
		}
		if err := json.Unmarshal(jsonData, &result); err != nil {
			continue // or handle error appropriately
		}
		results = append(results, result)
	}

	return results, nil
}
