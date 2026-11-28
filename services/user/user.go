package user

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/adapters/driven/firestore/fsutil"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/utils"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

type Service interface {
	GetUser(ctx context.Context, ID string) (*User, error)
	GetByIDs(ctx context.Context, userIDs []string) ([]User, error)
	FindByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]User, error)
	FindByCharacterIDs(ctx context.Context, characterIDs []string) ([]User, error)
	CreateUser(ctx context.Context, user *User) (*User, error)
	GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error)
	GetPrimaryMembership(ctx context.Context, userID string) (int64, int64, error)
	// BackfillCharacters fetches characters from Destiny for the user's primary membership
	// and updates the user's characterIds field in Firestore.
	BackfillCharacters(ctx context.Context, userID string) error
	UpdateCharacters(ctx context.Context, userID string, characters []api.Character) error
	UpdateSearchFields(ctx context.Context, userID string, displayName string, uniqueName string) error
	// GetAll returns all users. Used for admin backfills.
	GetAll(ctx context.Context) ([]User, error)
	// GetByCharacterID returns the user that owns the provided characterID. If not found returns (nil, nil).
	GetByCharacterID(ctx context.Context, characterID string) (*User, error)
	Search(ctx context.Context, query string, page int) ([]api.SearchUserResult, bool, error)
}
type userService struct {
	db            *firestore.Client
	d2Service     destiny.Service
	store         userStore
	bungieTimeout time.Duration
}

var _ Service = (*userService)(nil)

const (
	userCollection         = "users"
	characterSubCollection = "characters"
)

func NewUserService(client *firestore.Client, service destiny.Service) Service {
	return &userService{
		db:        client,
		d2Service: service,
		store:     newFirestoreUserStore(client),
	}
}

var NotFound = errors.New("user not found")

func (s *userService) GetUser(ctx context.Context, ID string) (*User, error) {
	clog.WithGroupField(ctx, "userService.getUser", "lookupId", ID)
	if s.store == nil {
		return nil, NotFound
	}
	u, err := s.store.findUserByLookupID(ctx, ID)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "userService.getUser", "userId", u.ID)
	return u, nil
}

func (s *userService) GetByIDs(ctx context.Context, userIDs []string) ([]User, error) {
	uniqueIDs := utils.UniqueTrimmedStrings(userIDs)
	if s.store == nil || len(uniqueIDs) == 0 {
		return []User{}, nil
	}
	return s.store.findUsersByIDs(ctx, uniqueIDs)
}

func (s *userService) FindByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]User, error) {
	uniqueIDs := utils.UniqueTrimmedStrings(membershipIDs)
	if s.store == nil || len(uniqueIDs) == 0 {
		return []User{}, nil
	}
	return s.store.findUsersByPartyMembershipIDs(ctx, uniqueIDs)
}

func (s *userService) FindByCharacterIDs(ctx context.Context, characterIDs []string) ([]User, error) {
	uniqueIDs := utils.UniqueTrimmedStrings(characterIDs)
	clog.WithGroupField(ctx, "userService.findByCharacterIds", "requestedCount", len(uniqueIDs))
	if s.store == nil || len(uniqueIDs) == 0 {
		clog.WithGroupField(ctx, "userService.findByCharacterIds", "returnedCount", 0)
		return []User{}, nil
	}
	users, err := s.store.findUsersByCharacterIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "userService.findByCharacterIds", "returnedCount", len(users))
	return users, nil
}

func (s *userService) CreateUser(ctx context.Context, user *User) (*User, error) {
	if user == nil {
		return nil, errors.New("user is nil")
	}

	now := time.Now()
	user.CreatedAt = now
	user.DisplayNameLower = strings.ToLower(strings.TrimSpace(user.DisplayName))
	user.UniqueNameLower = strings.ToLower(strings.TrimSpace(user.UniqueName))

	ref := s.db.Collection(userCollection).NewDoc()
	user.ID = ref.ID

	clog.WithGroupFields(ctx, "userService.createUser", clog.Fields{
		"userId":              user.ID,
		"memberId":            user.MemberID,
		"primaryMembershipId": user.PrimaryMembershipID,
	})

	_, err := ref.Set(ctx, user)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *userService) GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error) {
	clog.WithGroupFields(ctx, "userService.getMembershipType", clog.Fields{
		"userId":       userID,
		"membershipId": membershipID,
	})
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
	clog.WithGroupField(ctx, "userService.getMembershipType", "membershipType", membershipType)
	return membershipType, nil
}

func (s *userService) GetPrimaryMembership(ctx context.Context, userID string) (int64, int64, error) {
	clog.WithGroupField(ctx, "userService.getPrimaryMembership", "userId", userID)
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to fetch user: %w", err)
	}
	pmID, membershipType, err := u.PrimaryMembership()
	if err != nil {
		return 0, 0, err
	}
	clog.WithGroupFields(ctx, "userService.getPrimaryMembership", clog.Fields{
		"membershipId":   pmID,
		"membershipType": membershipType,
	})
	return pmID, membershipType, nil
}

// BackfillCharacters fetches the user's characters from Destiny and updates the
// characterIds array on the User document. This is useful for users created before
// character IDs were persisted or when data needs to be refreshed.
func (s *userService) BackfillCharacters(ctx context.Context, userID string) error {
	clog.WithGroupField(ctx, "userService.backfillCharacters", "userId", userID)
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to fetch user: %w", err)
	}
	pmId, membershipType, err := u.PrimaryMembership()
	if err != nil {
		return err
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
	clog.WithGroupField(ctx, "userService.backfillCharacters", "characterCount", len(charIDs))
	return nil
}

func (s *userService) UpdateCharacters(ctx context.Context, userID string, characters []api.Character) error {
	clog.WithGroupFields(ctx, "userService.updateCharacters", clog.Fields{
		"userId":         userID,
		"characterCount": len(characters),
	})
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
	results, err := fsutil.GetAllToStructs[User](docs)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "userService.getAll", "returnedCount", len(results))
	return results, nil
}

// GetByCharacterID returns the user that owns the provided characterID. If not found returns (nil, nil).
func (s *userService) GetByCharacterID(ctx context.Context, characterID string) (*User, error) {
	if characterID == "" {
		return nil, nil
	}
	clog.WithGroupField(ctx, "userService.getByCharacterId", "characterId", characterID)
	q := s.db.Collection(userCollection).
		Where("characterIds", "array-contains", characterID).
		Limit(1)
	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		clog.WithGroupField(ctx, "userService.getByCharacterId", "found", false)
		return nil, nil
	}
	u := &User{}
	if err := docs[0].DataTo(u); err != nil {
		return nil, err
	}
	clog.WithGroupFields(ctx, "userService.getByCharacterId", clog.Fields{
		"found":  true,
		"userId": u.ID,
	})
	return u, nil
}

func (s *userService) UpdateSearchFields(ctx context.Context, userID string, displayName string, uniqueName string) error {
	if userID == "" {
		return nil
	}
	clog.WithGroupField(ctx, "userService.updateSearchFields", "userId", userID)
	_, err := s.db.Collection(userCollection).Doc(userID).Set(ctx, map[string]any{
		"displayNameLower": strings.ToLower(strings.TrimSpace(displayName)),
		"uniqueNameLower":  strings.ToLower(strings.TrimSpace(uniqueName)),
	}, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to update user search fields: %w", err)
	}
	if fsStore, ok := s.store.(*firestoreUserStore); ok && fsStore != nil {
		fsStore.evictLegacyUser(userID)
	}
	return nil
}
