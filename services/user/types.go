package user

import (
	"errors"
	"fmt"
	"oneTrick/api"
	"strconv"
	"time"
)

type User struct {
	ID                    string          `json:"id" firestore:"id"`
	MemberID              string          `json:"memberId" firestore:"memberId"`
	PrimaryMembershipID   string          `json:"primaryMembershipId" firestore:"primaryMembershipId"`
	UniqueName            string          `json:"uniqueName" firestore:"uniqueName"`
	DisplayName           string          `json:"displayName" firestore:"displayName"`
	DisplayNameLower      string          `json:"displayNameLower,omitempty" firestore:"displayNameLower"`
	UniqueNameLower       string          `json:"uniqueNameLower,omitempty" firestore:"uniqueNameLower"`
	Memberships           []Membership    `json:"memberships" firestore:"memberships"`
	CreatedAt             time.Time       `json:"createdAt" firestore:"createdAt"`
	CharacterIDs          []string        `json:"characterIDs" firestore:"characterIds"`
	Characters            []api.Character `json:"characters" firestore:"characters"`
	LastUpdatedCharacters time.Time       `json:"lastUpdatedCharacters" firestore:"lastUpdatedCharacters"`
}

func (u *User) PrimaryMembership() (int64, int64, error) {
	if u == nil {
		return 0, 0, errors.New("user is nil")
	}
	if u.PrimaryMembershipID == "" {
		return 0, 0, errors.New("user missing primary membership id")
	}
	membershipType := int64(0)
	for _, membership := range u.Memberships {
		if membership.ID == u.PrimaryMembershipID {
			membershipType = membership.Type
			break
		}
	}
	if membershipType == 0 && len(u.Memberships) > 0 {
		membershipType = u.Memberships[0].Type
	}
	if membershipType == 0 {
		return 0, 0, errors.New("user missing valid membership type")
	}
	pmID, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse primary membership id: %w", err)
	}
	return pmID, membershipType, nil
}

type Membership struct {
	ID          string `json:"id" firestore:"id"`
	Type        int64  `json:"type" firestore:"type"`
	DisplayName string `json:"displayName" firestore:"displayName"`
}
